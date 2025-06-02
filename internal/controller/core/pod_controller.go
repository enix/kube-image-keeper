package core

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	PodImagesIndexKey = ".metadata.images"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pod object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	images := ImagesFromPod(ctx, &pod)

	for _, image := range images {
		var img kuikv1alpha1.Image
		err := r.Get(ctx, client.ObjectKeyFromObject(&image), &img)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !img.DeletionTimestamp.IsZero() {
			// Image is already scheduled for deletion, thus we don't have to handle it here and will enqueue it back later
			log.Info("image is already being deleted, skipping", "image", klog.KObj(&image))
			continue
		}

		// Create or update Image depending on weather it already exists or not
		if apierrors.IsNotFound(err) {
			err = r.Create(ctx, &image)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			patch := client.MergeFrom(img.DeepCopy())

			img.Spec.Name = image.Spec.Name

			if err = r.Patch(ctx, &img, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create an index to list Pods by Image
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, PodImagesIndexKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)

		logger := mgr.GetLogger().
			WithName("indexer.images.pods").
			WithValues("pod", klog.KObj(pod))
		ctx := logr.NewContext(context.Background(), logger)

		images := ImagesFromPod(ctx, pod)

		imageNames := make([]string, len(images))
		for _, image := range images {
			imageNames = append(imageNames, image.Name)
		}

		return imageNames
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("core-pod").
		Complete(r)
}

func ImagesFromPod(ctx context.Context, pod *corev1.Pod) []kuikv1alpha1.Image {
	images := imagesFromContainers(ctx, pod.Spec.Containers, pod.Annotations)
	images = append(images, imagesFromContainers(ctx, pod.Spec.InitContainers, pod.Annotations)...)
	return images
}

func imagesFromContainers(ctx context.Context, containers []corev1.Container, annotations map[string]string) []kuikv1alpha1.Image {
	log := logf.FromContext(ctx)
	images := []kuikv1alpha1.Image{}

	for _, container := range containers {
		containerLog := log.WithValues("container", container.Name)
		image, err := imageFromSourceImage(container.Image)
		if err != nil {
			containerLog.Error(err, "could not create image, ignoring")
			continue
		}
		images = append(images, *image)
	}

	return images
}

func imageFromSourceImage(sourceImage string) (*kuikv1alpha1.Image, error) {
	sanitizedName, err := kuikv1alpha1.ImageNameFromSourceImage(sourceImage)
	if err != nil {
		return nil, err
	}

	return &kuikv1alpha1.Image{
		TypeMeta: metav1.TypeMeta{APIVersion: kuikv1alpha1.GroupVersion.String(), Kind: "Image"},
		ObjectMeta: metav1.ObjectMeta{
			Name: sanitizedName,
		},
		Spec: kuikv1alpha1.ImageSpec{
			Name: sourceImage,
		},
	}, nil
}
