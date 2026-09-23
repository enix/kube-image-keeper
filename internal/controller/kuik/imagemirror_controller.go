package kuik

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// ImageMirrorReconciler reconciles a ImageMirror object
type ImageMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

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
