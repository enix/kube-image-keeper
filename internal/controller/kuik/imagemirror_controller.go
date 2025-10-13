package kuik

import (
	"context"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// ImageMirrorReconciler reconciles a ImageMirror object
type ImageMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ImageMirror object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ImageMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var imageMirror kuikv1alpha1.ImageMirror
	if err := r.Get(ctx, req.NamespacedName, &imageMirror); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !imageMirror.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	available := r.isImageAvailableOnTarget(logf.IntoContext(ctx, log), imageMirror)

	log.Info("checked availability", "available", available)

	if !available {
		if imageMirror.Status.Phase != "Mirroring" {
			patch := client.MergeFrom(imageMirror.DeepCopy())
			imageMirror.Status.Phase = "Mirroring"
			if err := r.Status().Patch(ctx, &imageMirror, patch); err != nil {
				return ctrl.Result{}, err
			}
		}

		err := r.mirrorImage(logf.IntoContext(ctx, log), imageMirror)
		if err != nil {
			patch := client.MergeFrom(imageMirror.DeepCopy())
			if imageMirror.Status.Phase != "Error" {
				imageMirror.Status.Phase = "Error"
				if err := r.Status().Patch(ctx, &imageMirror, patch); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, err
		}

		log.Info("image successfully mirrored")
	}

	if imageMirror.Status.Phase != "Ready" {
		patch := client.MergeFrom(imageMirror.DeepCopy())
		imageMirror.Status.Phase = "Ready"
		if err := r.Status().Patch(ctx, &imageMirror, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ImageMirrorReconciler) isImageAvailableOnTarget(ctx context.Context, imageMirror kuikv1alpha1.ImageMirror) bool {
	log := logf.FromContext(ctx)
	log.V(1).Info("verifying image availability on target registry", "reference", imageMirror.Spec.ImageReference)
	_, err := registry.NewClient(nil, nil).WithPullSecrets(nil).ReadDescriptor(http.MethodGet, imageMirror.TargetReference(), time.Second*30)
	return err == nil
}

func (r *ImageMirrorReconciler) mirrorImage(ctx context.Context, imageMirror kuikv1alpha1.ImageMirror) error {
	log := logf.FromContext(ctx)
	log.Info("mirroring image", "reference", imageMirror.Spec.ImageReference)
	return registry.MirrorImage(imageMirror.SourceReference(), imageMirror.TargetReference())
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMirror{}).
		Named("kuik-imagemirror").
		// prevent from reenquing after status update (produced a infinite loop between Error and Mirroring phases)
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
		)).
		Complete(r)
}
