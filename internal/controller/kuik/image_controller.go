package kuik

import (
	"context"
	"net/http"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
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

const (
	ImagesIndexKey = ".metadata.images"
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

	// update Pods and Nodes status for this Image
	if requeue, err := r.updateReferenceCount(ctx, &image); requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list Pods by Image
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, ImagesIndexKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		images, _ := kuikv1alpha1.ImagesFromPod(pod)

		imageNames := make([]string, len(images))
		for _, image := range images {
			imageNames = append(imageNames, image.Name)
		}

		return imageNames
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.Image{}).
		Named("kuik-image").
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.imagesRequestFromPod),
		).
		Complete(r)
}

// updateReferenceCount update Image UsedByPods status
func (r *ImageReconciler) updateReferenceCount(ctx context.Context, image *kuikv1alpha1.Image) (requeue bool, err error) {
	var podList corev1.PodList
	if err = r.List(ctx, &podList, client.MatchingFields{ImagesIndexKey: image.Name}); err != nil && !apierrors.IsNotFound(err) {
		return
	}

	pods := []string{}
	for _, pod := range podList.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		pods = append(pods, pod.Namespace+"/"+pod.Name)
	}

	image.Status.UsedByPods = kuikv1alpha1.ReferencesWithCount{
		Items: pods,
		Count: len(pods),
	}

	err = r.Status().Update(ctx, image)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			requeue = true
		}
		return
	}

	return
}

func (r *ImageReconciler) imagesRequestFromPod(ctx context.Context, obj client.Object) []ctrl.Request {
	pod := obj.(*corev1.Pod)
	images, _ := kuikv1alpha1.ImagesFromPod(pod)

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
