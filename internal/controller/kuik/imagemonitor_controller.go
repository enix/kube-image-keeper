package kuik

import (
	"context"
	"fmt"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/go-logr/logr"
	"github.com/mroth/weightedrand/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	RegristryIndexKey = ".spec.registry"
)

// ImageMonitorReconciler reconciles a ImageMonitor object
type ImageMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ImageMonitor object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ImageMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var imageMonitor kuikv1alpha1.ImageMonitor
	if err := r.Get(ctx, req.NamespacedName, &imageMonitor); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log = log.WithValues("registry", imageMonitor.Spec.Registry)

	timeAgo := metav1.Now().Sub(imageMonitor.Status.LastExecution.Time)
	log.Info("reconcile", "lastExecution", imageMonitor.Status.LastExecution)

	nextMonitor := imageMonitor.Spec.Interval.Duration - timeAgo
	if nextMonitor <= 0 {
		log.Info("monitoring images", "burst", imageMonitor.Spec.Burst)

		var images kuikv1alpha1.ImageList
		if err := r.List(ctx, &images, client.MatchingFields{
			RegristryIndexKey: imageMonitor.Spec.Registry,
		}); err != nil {
			return ctrl.Result{}, err
		}

		log.V(1).Info("found images for registry", "count", len(images.Items))

		choices := make([]weightedrand.Choice[kuikv1alpha1.Image, int], len(images.Items))
		totalCount := 0
		for _, image := range images.Items {
			choices = append(choices, weightedrand.NewChoice(image, image.Status.UsedByPods.Count))
			totalCount += image.Status.UsedByPods.Count
		}
		if totalCount == 0 {
			log.Info("no images to monitor, skipping")
			return reconcile.Result{RequeueAfter: nextMonitor}, nil
		}
		chooser, err := weightedrand.NewChooser(choices...)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create random chooser: %w", err)
		}

		for range imageMonitor.Spec.Burst {
			// FIXME: this can pick the same image multiple times
			image := chooser.Pick()
			log.V(1).Info("randomly selected an image to monitor", "image", image.Reference())

			// NOTE: this could fail, but we don't want to retry to prevent reaching the rate-limit
			r.monitorAnImage(ctx, log, &image)
		}

		patch := client.MergeFrom(imageMonitor.DeepCopy())
		imageMonitor.Status.LastExecution = metav1.Now()
		r.Status().Patch(ctx, &imageMonitor, patch)
	} else {
		log.Info("skipping monitoring images", "nextMonitor", nextMonitor)
		return ctrl.Result{RequeueAfter: nextMonitor}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list Images by registry
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.Image{}, RegristryIndexKey, func(rawObj client.Object) []string {
		image := rawObj.(*kuikv1alpha1.Image)

		return []string{image.Spec.Registry}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMonitor{}).
		Named("kuik-imagemonitor").
		Complete(r)
}

func (r *ImageMonitorReconciler) monitorAnImage(ctx context.Context, log logr.Logger, image *kuikv1alpha1.Image) {
	patch := client.MergeFrom(image.DeepCopy())
	defer func() {
		if err := r.Status().Patch(ctx, image, patch); err != nil {
			log.Info("failed to patch image status", "image", image.Reference(), "error", err.Error())
		}
	}()

	image.Status.Upstream.LastMonitor = metav1.Now()

	desc, err := registry.GetDescriptor(image.Reference(), nil, nil)
	if err != nil {
		log.Info("failed to get image descriptor, skipping", "image", image.Reference(), "error", err.Error())
		return
	}

	log.V(1).Info("image descriptor", "image", image.Reference(), "digest", desc.Digest, "size", desc.Size, "mediaType", desc.MediaType)
	image.Status.Upstream.LastSeen = metav1.Now()
	image.Status.Upstream.Digest = desc.Digest.String()
}
