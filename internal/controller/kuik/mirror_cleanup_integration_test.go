package kuik

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/google/go-containerregistry/pkg/crane"
	registryserver "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCleanupMirrorDeletesThroughRegistryWithoutSecret(t *testing.T) {
	isolateRegistryCredentials(t)
	fixture := newCleanupRegistryFixture(t)

	original := deleteMirrorImage
	t.Cleanup(func() { deleteMirrorImage = original })
	deleteMirrorImage = func(ctx context.Context, image string, secret *corev1.Secret) error {
		if secret != nil {
			t.Fatal("expected no explicit credential Secret")
		}
		return registry.NewClient([]string{fixture.host}, nil).DeleteImage(ctx, image)
	}

	reconciler := &ImageSetMirrorBaseReconciler{}
	if !reconciler.cleanupMirror(context.Background(), fixture.reference, "default", nil) {
		t.Fatal("cleanupMirror failed for an existing local image")
	}

	requests := fixture.snapshot()
	if countRequests(requests, http.MethodHead) != 1 {
		t.Fatalf("HEAD requests = %d, want 1", countRequests(requests, http.MethodHead))
	}
	if countRequests(requests, http.MethodDelete) != 1 {
		t.Fatalf("DELETE requests = %d, want 1", countRequests(requests, http.MethodDelete))
	}
	assertNoBasicAuth(t, requests)
}

func TestCleanupMirrorPassesConfiguredSecretToDeletion(t *testing.T) {
	image := "registry.example.com/test/image:tag"
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "explicit", Namespace: "default"}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&secret).Build()

	original := deleteMirrorImage
	t.Cleanup(func() { deleteMirrorImage = original })
	deleteMirrorImage = func(_ context.Context, _ string, gotSecret *corev1.Secret) error {
		if gotSecret == nil || gotSecret.Name != secret.Name {
			t.Fatalf("cleanup Secret = %#v, want %q", gotSecret, secret.Name)
		}
		return nil
	}

	reconciler := &ImageSetMirrorBaseReconciler{Client: client}
	mirrors := kuikv1alpha1.Mirrors{{
		Registry: "registry.example.com",
		Path:     "test",
		CredentialSecret: &kuikv1alpha1.CredentialSecret{
			Name: "explicit",
		},
	}}
	if !reconciler.cleanupMirror(context.Background(), image, "default", mirrors) {
		t.Fatal("cleanupMirror failed with the configured credential Secret")
	}
}

func TestRetentionCleanupFailureKeepsMirrorAndReturnsError(t *testing.T) {
	image := "registry.example.com/test/image:tag"
	resource := &kuikv1alpha1.ClusterImageSetMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "retention-failure"},
		Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
			ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{`registry\.example\.com/test/image:.*`}},
			Cleanup:     kuikv1alpha1.Cleanup{Enabled: true, Retention: metav1.Duration{Duration: time.Hour}},
			Mirrors:     kuikv1alpha1.Mirrors{{Registry: "mirror.example.com", Path: "cache"}},
		},
		Status: kuikv1alpha1.ClusterImageSetMirrorStatus{
			MatchingImages: []kuikv1alpha1.MatchingImage{{
				Image: image,
				Mirrors: []kuikv1alpha1.MirrorStatus{{
					Image:      "mirror.example.com/cache/test/image:tag",
					MirroredAt: &metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
				}},
				UnusedSince: &metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
			}},
		},
	}

	r, k8sClient := newClusterCleanupTestReconciler(t, resource)
	installFailingDeleter(t)

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(resource)})
	if err == nil {
		t.Fatal("retention reconciliation succeeded despite deletion failure")
	}

	got := &kuikv1alpha1.ClusterImageSetMirror{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resource), got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.MatchingImages) != 1 || len(got.Status.MatchingImages[0].Mirrors) != 1 {
		t.Fatal("failed retention cleanup removed the mirror from status")
	}
}

func TestFinalizerCleanupFailureKeepsFinalizerAndReturnsError(t *testing.T) {
	resource := &kuikv1alpha1.ClusterImageSetMirror{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "finalizer-failure",
			Finalizers:        []string{imageSetMirrorFinalizer},
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
		Status: kuikv1alpha1.ClusterImageSetMirrorStatus{
			MatchingImages: []kuikv1alpha1.MatchingImage{{
				Image: "registry.example.com/test/image:tag",
				Mirrors: []kuikv1alpha1.MirrorStatus{{
					Image:      "mirror.example.com/cache/test/image:tag",
					MirroredAt: &metav1.Time{Time: time.Now()},
				}},
			}},
		},
	}

	r, k8sClient := newClusterCleanupTestReconciler(t, resource)
	installFailingDeleter(t)

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(resource)})
	if err == nil {
		t.Fatal("finalizer reconciliation succeeded despite deletion failure")
	}

	got := &kuikv1alpha1.ClusterImageSetMirror{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resource), got); err != nil {
		t.Fatal(err)
	}
	if !containsString(got.Finalizers, imageSetMirrorFinalizer) {
		t.Fatal("failed finalizer cleanup removed the finalizer")
	}
}

type cleanupRegistryFixture struct {
	server    *httptest.Server
	host      string
	reference string
	backend   http.Handler
	mu        sync.Mutex
	requests  []cleanupRequest
}

type cleanupRequest struct {
	method   string
	path     string
	username string
}

func newCleanupRegistryFixture(t *testing.T) *cleanupRegistryFixture {
	t.Helper()
	fixture := &cleanupRegistryFixture{backend: registryserver.New()}
	fixture.server = httptest.NewServer(fixture)
	fixture.host = strings.TrimPrefix(fixture.server.URL, "http://")
	fixture.reference = fixture.host + "/test/image:tag"
	t.Cleanup(fixture.server.Close)

	image, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(image, fixture.reference); err != nil {
		t.Fatal(err)
	}
	fixture.reset()
	return fixture
}

func (f *cleanupRegistryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, _, _ := r.BasicAuth()
	f.mu.Lock()
	f.requests = append(f.requests, cleanupRequest{method: r.Method, path: r.URL.Path, username: username})
	f.mu.Unlock()

	f.backend.ServeHTTP(w, r)
}

func (f *cleanupRegistryFixture) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

func (f *cleanupRegistryFixture) snapshot() []cleanupRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cleanupRequest(nil), f.requests...)
}

func countRequests(requests []cleanupRequest, method string) int {
	count := 0
	for _, request := range requests {
		if request.method == method {
			count++
		}
	}
	return count
}

func assertNoBasicAuth(t *testing.T, requests []cleanupRequest) {
	t.Helper()
	for _, request := range requests {
		if request.username != "" {
			t.Fatalf("unexpected BasicAuth username %q on %s %s", request.username, request.method, request.path)
		}
	}
}

func isolateRegistryCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, variable := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_ROLE_ARN",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AZURE_CLIENT_ID",
		"AZURE_CLIENT_SECRET",
		"AZURE_TENANT_ID",
		"AZURE_FEDERATED_TOKEN_FILE",
	} {
		t.Setenv(variable, "")
	}
}

func newClusterCleanupTestReconciler(t *testing.T, resource *kuikv1alpha1.ClusterImageSetMirror) (*ClusterImageSetMirrorReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kuikv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource).WithObjects(resource).Build()
	return &ClusterImageSetMirrorReconciler{
		ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
			Client: k8sClient,
			Scheme: scheme,
		},
	}, k8sClient
}

func installFailingDeleter(t *testing.T) {
	t.Helper()
	original := deleteMirrorImage
	t.Cleanup(func() { deleteMirrorImage = original })
	deleteMirrorImage = func(context.Context, string, *corev1.Secret) error {
		return errors.New("simulated registry deletion failure")
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
