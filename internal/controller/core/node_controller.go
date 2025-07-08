package core

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Node object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	images := ImagesFromNode(ctx, &node)

	hasDeletingImages := false
	for _, image := range images {
		var img kuikv1alpha1.Image
		err := r.Get(ctx, client.ObjectKeyFromObject(&image), &img)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !img.DeletionTimestamp.IsZero() {
			hasDeletingImages = true
			// image is scheduled for deletion, thus we don't have to handle it here and will enqueue it back later
			log.Info("image is scheduled for deletion, skipping", "image", klog.KObj(&image))
			continue
		}

		// create or update Image depending on weather it already exists or not
		if apierrors.IsNotFound(err) {
			log.Info("new image found on a node", "image", klog.KObj(&image))
			err = r.Create(ctx, &image)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else if img.Reference() != image.Reference() {
			log.Info("image found with an invalid reference, patching it", "image", klog.KObj(&image))

			patch := client.MergeFrom(img.DeepCopy())

			img.Spec.Registry = image.Spec.Registry
			img.Spec.Image = image.Spec.Image

			if err = r.Patch(ctx, &img, patch); err != nil {
				return ctrl.Result{}, err
			}
		}

	}

	if hasDeletingImages {
		log.Info("some images are being deleted, requeuing later")
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	p := predicate.Not(predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newImage := e.ObjectNew.(*kuikv1alpha1.Image)
			oldImage := e.ObjectOld.(*kuikv1alpha1.Image)
			return newImage.Reference() == oldImage.Reference()
		},
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Named("core-node").
		Watches(
			&kuikv1alpha1.Image{},
			handler.EnqueueRequestsFromMapFunc(r.nodesUsingThisImage),
			builder.WithPredicates(p),
		).
		Complete(r)
}

func (r *NodeReconciler) nodesUsingThisImage(ctx context.Context, obj client.Object) []ctrl.Request {
	image := obj.(*kuikv1alpha1.Image)
	res := make([]ctrl.Request, len(image.Status.AvailableOnNodes.Items))

	for i, item := range image.Status.AvailableOnNodes.Items {
		res[i].NamespacedName = client.ObjectKey{Namespace: "", Name: item}
	}

	return res
}

func ImagesFromNode(ctx context.Context, node *corev1.Node) []kuikv1alpha1.Image {
	log := logf.FromContext(ctx)
	images := []kuikv1alpha1.Image{}

	for _, localImage := range node.Status.Images {
		for _, name := range localImage.Names {
			image, err := kuikv1alpha1.ImageFromReference(name)
			if err != nil {
				log.Error(err, "could not parse image, ignoring")
				continue
			}
			images = append(images, *image)
		}
	}

	return images
}
