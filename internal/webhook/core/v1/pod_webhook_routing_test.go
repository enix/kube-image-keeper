package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/maypok86/otter"
	"go4.org/syncutil/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Use actual registry HTTP probes so authentication failures and deduplication
// are exercised rather than mocking checkImageAvailabilityCached.
func routingRegistry(t *testing.T, authenticated bool, delay time.Duration) (string, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticated {
			username, password, _ := r.BasicAuth()
			if username != "reader" || password != "allowed" {
				w.Header().Set("WWW-Authenticate", `Basic realm="routing-test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if strings.Contains(r.URL.Path, "/manifests/") {
			reads.Add(1)
			time.Sleep(delay)
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("a", 64))
			w.Header().Set("Content-Length", "512")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), reads
}

func routingDefaulter(t *testing.T, objects ...client.Object) *PodCustomDefaulter {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := kuikv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	configuration, err := config.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := otter.MustBuilder[string, bool](1000).WithTTL(time.Second).Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	return &PodCustomDefaulter{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
		Config: configuration, checkCache: cache, requestGroup: &singleflight.Group{},
		cleanupSemaphore: make(chan struct{}, configuration.Routing.ActiveCheck.StaleMirrorCleanup.MaxConcurrent),
	}
}

func routingSecret(namespace, host, password string) *corev1.Secret {
	data, _ := json.Marshal(map[string]any{
		"auths": map[string]any{host: map[string]string{"username": "reader", "password": password}},
	})
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "reader", UID: types.UID("reader-" + namespace), ResourceVersion: "1"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: data},
	}
}

func routingRIS(namespace, host, mirrorPath string) *kuikv1alpha1.ReplicatedImageSet {
	return &kuikv1alpha1.ReplicatedImageSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "routing"},
		Spec: kuikv1alpha1.ReplicatedImageSetSpec{
			Priority: -1,
			Upstreams: []kuikv1alpha1.ReplicatedUpstream{
				{ImageReference: kuikv1alpha1.ImageReference{Registry: host, Path: mirrorPath}, ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"/" + mirrorPath + "/.*"}}, CredentialSecret: &kuikv1alpha1.CredentialSecret{Name: "reader"}},
				{ImageReference: kuikv1alpha1.ImageReference{Registry: host, Path: "origin"}, ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"/origin/.*"}}},
			},
		},
	}
}

func routingPod(namespace, reference string, policy corev1.PullPolicy) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: reference, ImagePullPolicy: policy}}},
	}
}

func TestRoutingNamespaceIsolation(t *testing.T) {
	host, _ := routingRegistry(t, false, 0)
	d := routingDefaulter(t, routingRIS("a", host, "mirror-a"), routingRIS("b", host, "mirror-b"),
		routingSecret("a", host, "allowed"), routingSecret("b", host, "allowed"))
	original := host + "/origin/image:latest"
	// Warm the old image-only selection cache before testing the other namespace.
	warm := routingPod("a", original, corev1.PullIfNotPresent)
	if err := d.defaultPod(context.Background(), warm, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			namespace := []string{"a", "b"}[i%2]
			pod := routingPod(namespace, original, corev1.PullIfNotPresent)
			if err := d.defaultPod(context.Background(), pod, true); err != nil {
				t.Error(err)
				return
			}
			if got, want := pod.Spec.Containers[0].Image, host+"/mirror-"+namespace+"/image:latest"; got != want {
				t.Errorf("namespace %s image = %s, want %s", namespace, got, want)
			}
			if got := pod.Spec.ImagePullSecrets; len(got) != 1 || got[0].Name != "reader" {
				t.Errorf("namespace %s must retain its local reader Secret, got %v", namespace, got)
			}
		})
	}
	wg.Wait()
}

func TestRoutingPolicyAndCRUpdates(t *testing.T) {
	host, _ := routingRegistry(t, false, 0)
	ris := routingRIS("a", host, "mirror-a")
	d := routingDefaulter(t, ris, routingSecret("a", host, "allowed"))
	original := host + "/origin/image:latest"
	for _, test := range []struct {
		policy corev1.PullPolicy
		want   string
	}{
		{corev1.PullIfNotPresent, host + "/mirror-a/image:latest"},
		{corev1.PullAlways, original},
	} {
		pod := routingPod("a", original, test.policy)
		if err := d.defaultPod(context.Background(), pod, true); err != nil {
			t.Fatal(err)
		}
		if got := pod.Spec.Containers[0].Image; got != test.want {
			t.Fatalf("policy %s image = %s, want %s", test.policy, got, test.want)
		}
	}
	// A CR update must affect the next Pod immediately, within the cache TTL.
	if err := d.Get(context.Background(), client.ObjectKeyFromObject(ris), ris); err != nil {
		t.Fatal(err)
	}
	ris.Spec.Upstreams[0].Path = "mirror-new"
	if err := d.Update(context.Background(), ris); err != nil {
		t.Fatal(err)
	}
	pod := routingPod("a", original, corev1.PullIfNotPresent)
	if err := d.defaultPod(context.Background(), pod, true); err != nil {
		t.Fatal(err)
	}
	if got, want := pod.Spec.Containers[0].Image, host+"/mirror-new/image:latest"; got != want {
		t.Fatalf("updated CR image = %s, want %s", got, want)
	}
}

func TestRoutingCredentialIsolationAndRotation(t *testing.T) {
	host, _ := routingRegistry(t, true, 0)
	allowed := routingSecret("a", host, "allowed")
	denied := routingSecret("b", host, "denied")
	d := routingDefaulter(t, routingRIS("a", host, "mirror"), routingRIS("b", host, "mirror"), allowed, denied)
	original := host + "/origin/image:latest"
	checkRoute := func(namespace, want string) {
		t.Helper()
		pod := routingPod(namespace, original, corev1.PullIfNotPresent)
		if err := d.defaultPod(context.Background(), pod, true); err != nil {
			t.Fatal(err)
		}
		if got := pod.Spec.Containers[0].Image; got != want {
			t.Fatalf("namespace %s image = %s, want %s", namespace, got, want)
		}
	}
	checkRoute("a", host+"/mirror/image:latest")
	checkRoute("b", original)
	// Update the same Secret twice, without waiting for the availability TTL.
	if err := d.Get(context.Background(), client.ObjectKeyFromObject(allowed), allowed); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"denied", "allowed"} {
		allowed.Data = routingSecret("a", host, password).Data
		if err := d.Update(context.Background(), allowed); err != nil {
			t.Fatal(err)
		}
		want := original
		if password == "allowed" {
			want = host + "/mirror/image:latest"
		}
		checkRoute("a", want)
	}
}

func TestAvailabilitySameContextDeduplication(t *testing.T) {
	host, reads := routingRegistry(t, false, 50*time.Millisecond)
	d := routingDefaulter(t)
	image := &AlternativeImage{Reference: host + "/image:latest"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			if err := d.checkImageAvailabilityCached(context.Background(), image, nil); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if err := d.checkImageAvailabilityCached(context.Background(), image, nil); err != nil {
		t.Fatal(err)
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("same-context concurrent probes and cache hit issued %d manifest requests, want 1", got)
	}
}

func TestAvailabilitySecretAppendIsolation(t *testing.T) {
	host, _ := routingRegistry(t, true, 0)
	d := routingDefaulter(t)
	allowed := routingSecret("a", host, "allowed")
	denied := routingSecret("a", host, "denied")
	// Spare capacity used to let parallel alternatives overwrite each other's
	// appended credential even though their slice headers were independent.
	pullSecrets := make([]corev1.Secret, 0, 1)
	for range 16 {
		container := &Container{Images: []AlternativeImage{
			{Reference: host + "/preferred/image:latest", ImagePullSecret: allowed},
			{Reference: host + "/fallback/image:latest", ImagePullSecret: denied},
		}}
		selected, errs := d.findBestAlternative(context.Background(), container, pullSecrets)
		if selected == nil || selected.Reference != container.Images[0].Reference {
			t.Fatalf("preferred authenticated alternative was lost: %v", errs)
		}
	}
	if got := pullSecrets[:1][0].Name; got != "" {
		t.Fatalf("parallel alternatives mutated the caller's spare Secret slice: %s", got)
	}
}
