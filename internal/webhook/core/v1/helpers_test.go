package v1

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	// clusterResourceNamespace is where the specs' secretRefs resolve.
	clusterResourceNamespace = "kuik-system"
	// clusterID suffixes the mirror tags.
	clusterID = "cluster-a"
	// repository is the image path every registry of a spec serves.
	repository = "library/nginx:1.27"
	// The user and password of the registries that require credentials.
	registryUser     = "kuik"
	registryPassword = "secret"

	// appContainer is the name of the first container of newPod.
	appContainer = "app"
	// injected is an image another mutating webhook writes over kuik's.
	injected = "other.tld/injected:v1"
	// podCreds is the pull secret the specs' pods declare.
	podCreds = "pod-creds"
	// teamLabel is the namespace label the specs select on.
	teamLabel = "team"
)

// testConfig is the global config of the specs: the spec's defaults, a short timeout.
func testConfig() *config.Config {
	return &config.Config{
		ClusterID: clusterID,
		Webhook: config.Webhook{
			DemoteMirrorWithPullPolicyAlways: true,
			AvailabilityCheck: config.AvailabilityCheck{
				Timeout:          config.Duration{Duration: 500 * time.Millisecond},
				ActiveCheckCache: config.ActiveCheckCache{TTL: config.Duration{Duration: 10 * time.Second}},
			},
		},
	}
}

// newDefaulter returns a webhook reading with the envtest API server and the given config.
func newDefaulter(cfg *config.Config) *PodDefaulter {
	return NewPodDefaulter(Dependencies{
		Namespaces:               k8sClient,
		Secrets:                  k8sClient,
		ClusterResourceNamespace: clusterResourceNamespace,
		Registry:                 registry.NewClient(),
		Config:                   cfg,
	})
}

// newRegistry starts a registry serving repository, closed when the spec ends. It forgets
// the requests of the push, so that a spec counts those of the admission alone.
func newRegistry(opts ...registrytest.Option) *registrytest.Registry {
	r := registrytest.New(opts...)
	DeferCleanup(r.Close)
	r.Push(repository, image)
	r.Reset()
	return r
}

// image is the image every registry serves, so that a digest is the same everywhere.
var image = registrytest.Image()

// ref is the reference of repository on r.
func ref(r *registrytest.Registry) string {
	return r.Host() + "/" + repository
}

// group is the repositoryGroup of repository on r.
func group(r *registrytest.Registry) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{RepositoryGroup: r.Host() + "/library"}
}

// manifestHeads counts the manifest HEADs r received.
func manifestHeads(r *registrytest.Registry) int {
	return len(r.Requests(http.MethodHead, "/manifests/"))
}

// fail makes every manifest HEAD on r answer 404.
func fail(r *registrytest.Registry) {
	r.Intercept(registrytest.Status(http.MethodHead, "/manifests/", http.StatusNotFound, nil))
}

// delay makes every manifest HEAD on r wait d, then lets the registry answer.
func delay(r *registrytest.Registry, d time.Duration) {
	r.Intercept(func(w http.ResponseWriter, req *http.Request) bool {
		if req.Method == http.MethodHead {
			select {
			case <-time.After(d):
			case <-req.Context().Done():
			}
		}
		return false
	})
}

func alternative(name string, policy kuikv1alpha1.RewritePolicy, entries ...kuikv1alpha1.Alternative) kuikv1alpha1.ImageAlternative {
	return kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       kuikv1alpha1.ImageAlternativeSpec{RewritePolicy: policy, Alternatives: entries},
	}
}

func imageMirror(name string, policy kuikv1alpha1.RewritePolicy, destination *registrytest.Registry) kuikv1alpha1.ImageMirror {
	return kuikv1alpha1.ImageMirror{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kuikv1alpha1.ImageMirrorSpec{
			RewritePolicy: policy,
			Destination:   kuikv1alpha1.MirrorDestination{Path: destination.Host() + "/mirror/"},
		},
	}
}

// mirrored is the reference an ImageMirror to destination serves origin's repository at,
// pushed there so that the mirror candidate answers.
func mirrored(destination, origin *registrytest.Registry) string {
	path := "mirror/" + strings.Replace(origin.Host(), ":", "_", 1) + "/" + repository + "_" + clusterID
	destination.Push(path, image)
	return destination.Host() + "/" + path
}

func secretAuth(name string) *kuikv1alpha1.Auth {
	return &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

func withAuth(entry kuikv1alpha1.Alternative, auth *kuikv1alpha1.Auth) kuikv1alpha1.Alternative {
	entry.Auth = auth
	return entry
}

func injectPullSecret(auth *kuikv1alpha1.Auth, inject bool) *kuikv1alpha1.Auth {
	auth.InjectPullSecret = &inject
	return auth
}

// newNamespace creates a namespace with labels, deleted when the spec ends.
func newNamespace(labels map[string]string) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "routing-", Labels: labels}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })
	return ns.Name
}

// ensureClusterResourceNamespace creates the cluster resource namespace if it is missing.
func ensureClusterResourceNamespace() {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterResourceNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// createDockerConfigSecret creates a docker-registry Secret holding the registries'
// credentials for host, deleted when the spec ends.
func createDockerConfigSecret(namespace, name, host string) {
	auth := base64.StdEncoding.EncodeToString([]byte(registryUser + ":" + registryPassword))
	dockerConfig := fmt.Sprintf(`{"auths":{%q:{"username":%q,"password":%q,"auth":%q}}}`, host, registryUser, registryPassword, auth)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerConfig)},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })
}

// newPod is a pod in namespace whose containers run images, named after their position:
// `app` for the first, `app-1`, `app-2`... for the next ones.
func newPod(namespace string, images ...string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, GenerateName: "routed-", Labels: map[string]string{"tier": "web"}},
	}
	for i, img := range images {
		name := appContainer
		if i > 0 {
			name = fmt.Sprintf("app-%d", i)
		}
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
			Name: name, Image: img, ImagePullPolicy: corev1.PullIfNotPresent,
		})
	}
	return pod
}

// admit runs d on a copy of pod, as the API server does on a CREATE in namespace, and
// returns the mutated copy. Default never fails: kuik fails open.
func admit(d *PodDefaulter, namespace string, pod *corev1.Pod) *corev1.Pod {
	GinkgoHelper()
	mutated := pod.DeepCopy()
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Namespace: namespace,
		Operation: admissionv1.Create,
	}}
	Expect(d.Default(admission.NewContextWithRequest(ctx, req), mutated)).To(Succeed())
	return mutated
}

// rewrites reads kuik.enix.io/rewrites or kuik.enix.io/conceded-rewrites.
func rewrites(pod *corev1.Pod, annotation string) map[string]Rewrite {
	GinkgoHelper()
	entries := map[string]Rewrite{}
	if value, ok := pod.Annotations[annotation]; ok {
		Expect(json.Unmarshal([]byte(value), &entries)).To(Succeed())
	}
	return entries
}

// noAlternatives reads kuik.enix.io/no-alternatives.
func noAlternatives(pod *corev1.Pod) map[string][]string {
	GinkgoHelper()
	entries := map[string][]string{}
	if value, ok := pod.Annotations[AnnotationNoAlternatives]; ok {
		Expect(json.Unmarshal([]byte(value), &entries)).To(Succeed())
	}
	return entries
}

// pullSecrets lists the names of the pod's imagePullSecrets.
func pullSecrets(pod *corev1.Pod) []string {
	names := make([]string, 0, len(pod.Spec.ImagePullSecrets))
	for _, s := range pod.Spec.ImagePullSecrets {
		names = append(names, s.Name)
	}
	return names
}
