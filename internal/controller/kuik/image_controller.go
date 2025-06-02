package kuik

import (
	"context"
	"net/http"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/controller/core"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ImageReconciler reconciles a Image object
type ImageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=images,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=images/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=images/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Image object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	var image kuikv1alpha1.Image
	if err := r.Get(ctx, req.NamespacedName, &image); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// update Image UsedBy status
	if requeue, err := r.updatePodCount(ctx, &image); requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.Image{}).
		Named("kuik-image").
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.cachedImagesRequestFromPod),
		).
		Complete(r)
}

// updatePodCount update CachedImage UsedBy status
func (r *ImageReconciler) updatePodCount(ctx context.Context, cachedImage *kuikv1alpha1.Image) (requeue bool, err error) {
	var podsList corev1.PodList
	if err = r.List(ctx, &podsList, client.MatchingFields{core.PodImagesIndexKey: cachedImage.Name}); err != nil && !apierrors.IsNotFound(err) {
		return
	}

	pods := []kuikv1alpha1.PodReference{}
	for _, pod := range podsList.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		pods = append(pods, kuikv1alpha1.PodReference{NamespacedName: pod.Namespace + "/" + pod.Name})
	}

	cachedImage.Status.UsedBy = kuikv1alpha1.UsedBy{
		Pods:  pods,
		Count: len(pods),
	}

	err = r.Status().Update(ctx, cachedImage)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			requeue = true
		}
		return
	}

	return
}

func (r *ImageReconciler) cachedImagesRequestFromPod(ctx context.Context, obj client.Object) []ctrl.Request {
	pod := obj.(*corev1.Pod)
	images := core.ImagesFromPod(ctx, pod)

	res := []ctrl.Request{}
	for _, image := range images {
		res = append(res, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name: image.Name,
			},
		})
	}

	return res
}
