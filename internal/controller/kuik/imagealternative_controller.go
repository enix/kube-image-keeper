package kuik

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// DefaultStatusInterval is how long a pod change waits before the status it affects is
// written: a rollout cycles through thousands of pods, and writing per event would storm the
// API server, so a burst of pod events writes each status once.
const DefaultStatusInterval = 10 * time.Second

// ImageAlternativeReconciler writes the status of the ImageAlternatives: whether each one is
// usable, and the routing side read off the pods it selects.
type ImageAlternativeReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// StatusInterval delays the report a pod or namespace change asks for. Zero means
	// DefaultStatusInterval.
	StatusInterval time.Duration
}

// ImageAlternativeOptions are what an ImageAlternativeReconciler reads and reports with.
type ImageAlternativeOptions struct {
	// APIReader reads the Secrets a secretRef names, uncached.
	APIReader client.Reader
	// ClusterResourceNamespace is where a secretRef resolves.
	ClusterResourceNamespace string
	// Recorder emits the events, on the resources and on the pods.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
}

// NewImageAlternativeReconciler returns a reconciler writing with c.
func NewImageAlternativeReconciler(c client.Client, scheme *runtime.Scheme, opts ImageAlternativeOptions) (*ImageAlternativeReconciler, error) {
	return &ImageAlternativeReconciler{Client: c, Scheme: scheme}, nil
}

// Elected records when the lease was acquired: pod events go only to pods created since.
func (r *ImageAlternativeReconciler) Elected(at time.Time) {}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives/status,verbs=update;patch,roleName=reconciler

// Reconcile writes the status of one ImageAlternative.
func (r *ImageAlternativeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageAlternativeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageAlternative{}).
		Named("kuik-imagealternative").
		Complete(r)
}
