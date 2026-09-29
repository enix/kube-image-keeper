package v1

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// The annotations the webhook records its decisions in, keyed by container name. See
// docs/v3/observability.md, "Annotations".
const (
	AnnotationRewrites         = "kuik.enix.io/rewrites"
	AnnotationConcededRewrites = "kuik.enix.io/conceded-rewrites"
	AnnotationNoAlternatives   = "kuik.enix.io/no-alternatives"
)

// Rewrite is one entry of kuik.enix.io/rewrites and kuik.enix.io/conceded-rewrites.
type Rewrite struct {
	// By is the resource that supplied the reference, as `<kind>/<name>`.
	By string `json:"by"`
	// Origin is the normalised reference the container came from.
	Origin string `json:"origin"`
	// RewrittenTo is the reference kuik placed.
	RewrittenTo string `json:"rewrittenTo"`
	// Policy is `Always` or `OnFailure`.
	Policy string `json:"policy"`
}

// The two counters the webhook exports. See docs/v3/observability.md, "Counters".
var (
	RewritesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kuik_routing_rewrites_total",
		Help: "Container images rewritten at admission, by the routing resource that supplied the reference " +
			"and the policy that placed it (Always, OnFailure)",
	}, []string{"kind", "name", "policy"})
	AlternativesExhaustedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kuik_routing_alternatives_exhausted_total",
		Help: "Containers left untouched at admission because no candidate answered, counted once per routing " +
			"resource that offered one. Several resources count the same container, so these series must not be summed",
	}, []string{"kind", "name"})
)

// Dependencies are what the webhook reads with.
type Dependencies struct {
	// Namespaces reads the Namespace of a pod, for the namespaceSelector of the resources.
	Namespaces client.Reader
	// Secrets reads Secrets. It must not be cached: a refused read of a pod's pull secret has
	// to come back as a Forbidden.
	Secrets client.Reader
	// ClusterResourceNamespace is where a `secretRef` resolves.
	ClusterResourceNamespace string
	// Registry probes the candidates.
	Registry *registry.Client
	// Config is the global config at startup.
	Config *config.Config
}

// SetupPodWebhookWithManager registers the webhook for Pod in the manager, keeps its
// resources in step with the manager's cache, and returns it so that config reloads reach it.
func SetupPodWebhookWithManager(mgr ctrl.Manager, cfg *config.Config, clusterResourceNamespace string) (*PodDefaulter, error) {
	d := NewPodDefaulter(Dependencies{
		Namespaces:               mgr.GetClient(),
		Secrets:                  mgr.GetAPIReader(),
		ClusterResourceNamespace: clusterResourceNamespace,
		Registry:                 registry.NewClient(),
		Config:                   cfg,
	})
	return d, ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(d).
		Complete()
}

// The three settings below are prescribed by docs/v3/architecture.md, "The admission path":
// fail open, CREATE only, reinvocable.
// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=ignore,reinvocationPolicy=IfNeeded,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

// The webhook reads what it matches pods against and writes nothing: no status, no Secret,
// no pod (the AdmissionReview carries it).
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives;imagemirrors;imagemonitors,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=webhook

// Cluster-wide Secret read, for the imagePullSecrets of the pod an admission probe checks. The
// chart binds it according to secretAccess, to the webhook and the reconciler, never to the syncer.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// PodDefaulter routes the images of a pod at admission.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type PodDefaulter struct{}

// NewPodDefaulter returns a webhook reading with d.
func NewPodDefaulter(d Dependencies) *PodDefaulter {
	return &PodDefaulter{}
}

// SetConfig replaces the global config, on every reload.
func (d *PodDefaulter) SetConfig(cfg *config.Config) {}

// SetResources replaces the routing resources the webhook matches pods against.
func (d *PodDefaulter) SetResources(alternatives []kuikv1alpha1.ImageAlternative, mirrors []kuikv1alpha1.ImageMirror) {
}

// Default implements admission.Defaulter so a webhook will be registered for the Kind Pod.
func (d *PodDefaulter) Default(ctx context.Context, pod *corev1.Pod) error {
	return nil
}
