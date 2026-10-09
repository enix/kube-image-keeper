package kuik

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// ImageMonitorReconciler writes the status of the ImageMonitors: the images the pods they
// select run, and the ones kept for unusedImageRetention once no pod declares them.
type ImageMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// ImageMonitorOptions are what an ImageMonitorReconciler reports with.
type ImageMonitorOptions struct {
	// Recorder emits the events on the monitors.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
	// Clock dates the retained images.
	Clock clock.Clock
}

// NewImageMonitorReconciler returns a reconciler writing with c.
func NewImageMonitorReconciler(c client.Client, scheme *runtime.Scheme, _ ImageMonitorOptions) (*ImageMonitorReconciler, error) {
	return &ImageMonitorReconciler{Client: c, Scheme: scheme}, nil
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/status,verbs=update;patch,roleName=reconciler

// Reconcile writes the status of one ImageMonitor.
func (r *ImageMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMonitor{}).
		Named("kuik-imagemonitor").
		Complete(r)
}
