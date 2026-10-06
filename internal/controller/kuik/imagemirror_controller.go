package kuik

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
)

// ImageMirrorReconciler reconciles a ImageMirror object
type ImageMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// ImageMirrorOptions are what an ImageMirrorReconciler reads, copies and reports with.
type ImageMirrorOptions struct {
	// APIReader reads the Secrets a secretRef names, uncached.
	APIReader client.Reader
	// ClusterResourceNamespace is where a secretRef resolves.
	ClusterResourceNamespace string
	// Recorder emits the events, on the mirrors and on the pods.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
	// Scheduler paces the copies and the drift checks against the source registries.
	Scheduler *pacing.Scheduler
	// Registry reads the sources and writes the destination.
	Registry *registry.Client
	// Config is the global config as loaded at start-up; SetConfig hands over a reload.
	Config *config.Config
	// Clock dates the destination passes and the status entries.
	Clock clock.Clock
}

// NewImageMirrorReconciler returns a reconciler writing with c.
func NewImageMirrorReconciler(c client.Client, scheme *runtime.Scheme, opts ImageMirrorOptions) (*ImageMirrorReconciler, error) {
	return &ImageMirrorReconciler{Client: c, Scheme: scheme}, nil
}

// Elected records when the lease was acquired: pod events go only to pods created since.
func (r *ImageMirrorReconciler) Elected(at time.Time) {}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors/status,verbs=update;patch,roleName=reconciler

// The reads the reconciler shares across its loops, declared once here, on the loop that
// talks to registries. Secrets are read in the install namespace (a Role: the chart puts it
// in the release namespace, kuik-system is a placeholder).
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=reconciler

// Cluster-wide Secret read, for the imagePullSecrets of the pods a check concerns. The chart
// binds it according to secretAccess, to the webhook and the reconciler, never to the syncer.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ImageMirror object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *ImageMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMirror{}).
		Named("kuik-imagemirror").
		Complete(r)
}
