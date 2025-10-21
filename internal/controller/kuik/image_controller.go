package kuik

import (
	"context"
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/routing"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ImagesIndexKey = ".metadata.images"
)

// ImageReconciler reconciles a Image object
type ImageReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	UnusedImageTTL time.Duration
	Config         *config.Config
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
	log := logf.FromContext(ctx)

	var image kuikv1alpha1.Image
	if err := r.Get(ctx, req.NamespacedName, &image); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log = log.WithValues("reference", image.Reference())

	if requeue, err := r.updateReferenceCount(logf.IntoContext(ctx, log), &image); requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if !image.Status.UnusedSince.IsZero() {
		if v1.Now().Sub(image.Status.UnusedSince.Time) > r.UnusedImageTTL {
			log.Info("image is unused for too long, deleting it", "unusedSince", image.Status.UnusedSince, "TTL", r.UnusedImageTTL)
			if err := r.Delete(ctx, &image); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		} else {
			return ctrl.Result{RequeueAfter: r.UnusedImageTTL - time.Since(image.Status.UnusedSince.Time)}, nil
		}
	}

	registries := routing.MatchingRegistries(r.Config, &image.Spec.ImageReference)
	for _, reg := range registries {
		if reg.MirroringEnabled {
			imageMirror := &kuikv1alpha1.ImageMirror{
				Spec: kuikv1alpha1.ImageMirrorSpec{
					ImageReference: image.Spec.ImageReference,
					TargetRegistry: reg.Url,
				},
			}

			name, err := registry.ImageNameFromReference(imageMirror.TargetReference())
			if err != nil {
				return ctrl.Result{}, err
			}
			imageMirror.Name = name

			op, err := controllerutil.CreateOrPatch(ctx, r.Client, imageMirror, func() error {
				if err := controllerutil.SetOwnerReference(&image, imageMirror, r.Scheme); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			if op != controllerutil.OperationResultNone {
				log.V(1).Info("ensured ImageMonitor", "operation", op, "ImageMonitor", map[string]string{"name": imageMirror.Name, "source": imageMirror.Spec.Reference(), "destination": imageMirror.Spec.TargetRegistry})
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// create an index to list Pods by Image
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, ImagesIndexKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		images, _ := kuikv1alpha1.ImagesFromPod(pod) // FIXME: log or handle errors in some way

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
		Owns(&kuikv1alpha1.ImageMirror{}, builder.MatchEveryOwner).
		Complete(r)
}

// updateReferenceCount update Image UsedByPods status
func (r *ImageReconciler) updateReferenceCount(ctx context.Context, image *kuikv1alpha1.Image) (requeue bool, err error) {
	log := logf.FromContext(ctx)

	var podList corev1.PodList
	if err = r.List(ctx, &podList, client.MatchingFields{ImagesIndexKey: image.Name}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
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

	if len(pods) == 0 {
		if image.Status.UnusedSince.IsZero() {
			image.Status.UnusedSince = v1.Now()
			log.V(1).Info("image has changed from in-use to unused")
		}
	} else {
		if !image.Status.UnusedSince.IsZero() {
			image.Status.UnusedSince = v1.Time{}
			log.V(1).Info("image has changed from unused to in-use")
		}
	}

	err = r.Status().Update(ctx, image)
	if err != nil {
		if statusErr, ok := err.(*apierrors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			requeue = true
		}
		return requeue, err
	}

	return false, nil
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
