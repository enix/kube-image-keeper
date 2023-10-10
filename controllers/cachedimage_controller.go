package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/enix/kube-image-keeper/api/v1alpha1"
	kuikenixiov1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// https://book.kubebuilder.io/reference/using-finalizers.html
const cachedImageFinalizerName = "cachedimage.kuik.enix.io/finalizer"

// CachedImageReconciler reconciles a CachedImage object
type CachedImageReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	ApiReader   client.Reader
	ExpiryDelay time.Duration
}

//+kubebuilder:rbac:groups=kuik.enix.io,resources=cachedimages,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kuik.enix.io,resources=cachedimages/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kuik.enix.io,resources=cachedimages/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CachedImage object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *CachedImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.
		FromContext(ctx)

	var cachedImage kuikenixiov1alpha1.CachedImage
	if err := r.Get(ctx, req.NamespacedName, &cachedImage); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("reconciling cachedimage")

	// Remove image from registry when CachedImage is being deleted, finalizer is removed after it
	if !cachedImage.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cachedImage, cachedImageFinalizerName) {
			log.Info("deleting image from cache")
			r.Recorder.Eventf(&cachedImage, "Normal", "CleaningUp", "Removing image %s from cache", cachedImage.Spec.SourceImage)
			if err := registry.DeleteImage(cachedImage.Spec.SourceImage); err != nil {
				r.Recorder.Eventf(&cachedImage, "Warning", "CleanupFailed", "Image %s could not be removed from cache: %s", cachedImage.Spec.SourceImage, err)
				return ctrl.Result{}, err
			}
			r.Recorder.Eventf(&cachedImage, "Normal", "CleanedUp", "Image %s successfully removed from cache", cachedImage.Spec.SourceImage)
			imageRemovedFromCache.Inc()

			log.Info("removing finalizer")
			controllerutil.RemoveFinalizer(&cachedImage, cachedImageFinalizerName)
			if err := r.Update(ctx, &cachedImage); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Add finalizer to keep the CachedImage during image removal from registry on deletion
	if !controllerutil.ContainsFinalizer(&cachedImage, cachedImageFinalizerName) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(&cachedImage, cachedImageFinalizerName)
		if err := r.Update(ctx, &cachedImage); err != nil {
			return ctrl.Result{}, err
		}
	}

	log = log.WithValues("sourceImage", cachedImage.Spec.SourceImage)

	// Update CachedImage UsedBy status
	if requeue, err := r.updatePodCount(ctx, &cachedImage); requeue {
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Set an expiration date for unused CachedImage
	expiresAt := cachedImage.Spec.ExpiresAt
	if len(cachedImage.Status.UsedBy.Pods) == 0 && !cachedImage.Spec.Retain {
		expiresAt := metav1.NewTime(time.Now().Add(r.ExpiryDelay))
		log.Info("cachedimage is no longer used, setting an expiry date", "cachedImage", klog.KObj(&cachedImage), "expiresAt", expiresAt)
		cachedImage.Spec.ExpiresAt = &expiresAt

		err := r.Patch(ctx, &cachedImage, client.Merge)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else {
		log.Info("cachedimage is used or retained", "cachedImage", klog.KObj(&cachedImage), "expiresAt", expiresAt, "retain", cachedImage.Spec.Retain)
		patch := client.MergeFrom(cachedImage.DeepCopy())
		cachedImage.Spec.ExpiresAt = nil
		err := r.Patch(ctx, &cachedImage, patch)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Delete expired CachedImage and schedule deletion for expiring ones
	if !expiresAt.IsZero() {
		if time.Now().After(expiresAt.Time) {
			log.Info("cachedimage expired, deleting it", "now", time.Now(), "expiresAt", expiresAt)
			r.Recorder.Eventf(&cachedImage, "Normal", "Expiring", "Image %s has expired, deleting it", cachedImage.Spec.SourceImage)
			err := r.Delete(ctx, &cachedImage)
			if err != nil {
				r.Recorder.Eventf(&cachedImage, "Warning", "ExpiringFailed", "Image %s could not expire: %s", cachedImage.Spec.SourceImage, err)
				return ctrl.Result{}, err
			}
			r.Recorder.Eventf(&cachedImage, "Normal", "Expired", "Image %s successfully expired", cachedImage.Spec.SourceImage)
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{RequeueAfter: time.Until(expiresAt.Time)}, nil
		}
	}

	// Adding image to registry
	log.Info("caching image")
	isCached, err := registry.ImageIsCached(cachedImage.Spec.SourceImage)
	if err != nil {
		log.Error(err, "could not determine if the image present in cache")
		return ctrl.Result{}, err
	}

	if !isCached {
		r.Recorder.Eventf(&cachedImage, "Normal", "Caching", "Start caching image %s", cachedImage.Spec.SourceImage)
		keychain := registry.NewKubernetesKeychain(r.ApiReader, cachedImage.Spec.PullSecretsNamespace, cachedImage.Spec.PullSecretNames)
		if err := registry.CacheImage(cachedImage.Spec.SourceImage, keychain); err != nil {
			log.Error(err, "failed to cache image")
			r.Recorder.Eventf(&cachedImage, "Warning", "CacheFailed", "Failed to cache image %s, reason: %s", cachedImage.Spec.SourceImage, err)
			return ctrl.Result{}, err
		} else {
			log.Info("image cached")
			r.Recorder.Eventf(&cachedImage, "Normal", "Cached", "Successfully cached image %s", cachedImage.Spec.SourceImage)
			imagePutInCache.Inc()
			if err := r.Get(ctx, req.NamespacedName, &cachedImage); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else {
		log.Info("image already present in cache, ignoring")
	}

	// Update CachedImage IsCached status
	log.Info("updating CachedImage status")
	cachedImage.Status.IsCached = true
	err = r.Status().Update(context.Background(), &cachedImage)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("reconciled cachedimage")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CachedImageReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles int) error {
	// Create an index to list Pods by CachedImage
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, cachedImageOwnerKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		if _, ok := pod.Labels[LabelImageRewrittenName]; !ok {
			return []string{}
		}

		logger := mgr.GetLogger().
			WithName("indexer.cachedimage.pods").
			WithValues("pod", klog.KObj(pod))
		ctx := logr.NewContext(context.Background(), logger)

		cachedImages := desiredCachedImages(ctx, pod)

		cachedImageNames := make([]string, len(cachedImages))
		for _, cachedImage := range cachedImages {
			cachedImageNames = append(cachedImageNames, cachedImage.Name)
		}

		return cachedImageNames
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikenixiov1alpha1.CachedImage{}).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			handler.EnqueueRequestsFromMapFunc(r.cachedImagesRequestFromPod),
			builder.WithPredicates(predicate.Funcs{
				// GenericFunc: func(e event.GenericEvent) bool {
				// 	return true
				// },
				DeleteFunc: func(e event.DeleteEvent) bool {
					pod := e.Object.(*corev1.Pod)
					var currentPod corev1.Pod
					// wait for the Pod to be really deleted
					err := r.Get(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &currentPod)
					return err != nil && apierrors.IsNotFound(err)
				},
			}),
		).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			handler.EnqueueRequestsFromMapFunc(r.cachedImagesRequestFromPod),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return true
				},
			}),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(r)
}

// updatePodCount update CachedImage UsedBy status
func (r *CachedImageReconciler) updatePodCount(ctx context.Context, cachedImage *kuikenixiov1alpha1.CachedImage) (requeue bool, err error) {
	var podsList corev1.PodList
	if err = r.List(ctx, &podsList, client.MatchingFields{cachedImageOwnerKey: cachedImage.Name}); err != nil && !apierrors.IsNotFound(err) {
		return
	}

	pods := []v1alpha1.PodReference{}
	for _, pod := range podsList.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		pods = append(pods, v1alpha1.PodReference{NamespacedName: pod.Namespace + "/" + pod.Name})
	}

	cachedImage.Status.UsedBy = v1alpha1.UsedBy{
		Pods:  pods,
		Count: len(pods),
	}

	err = r.Status().Update(context.Background(), cachedImage)
	if err != nil {
		if statusErr, ok := err.(*errors.StatusError); ok && statusErr.Status().Code == http.StatusConflict {
			requeue = true
		}
		return
	}

	return
}

func (r *CachedImageReconciler) cachedImagesRequestFromPod(obj client.Object) []ctrl.Request {
	log := log.
		FromContext(context.Background()).
		WithName("controller-runtime.manager.controller.cachedImage.deletingPods").
		WithValues("pod", klog.KObj(obj))

	pod := obj.(*corev1.Pod)
	ctx := logr.NewContext(context.Background(), log)
	cachedImages := desiredCachedImages(ctx, pod)

	res := []ctrl.Request{}
	for _, cachedImage := range cachedImages {
		for _, value := range pod.GetAnnotations() {
			// TODO check key format is "original-image-%s" or "original-init-image-%s"
			if cachedImage.Spec.SourceImage == value {
				res = append(res, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name: cachedImage.Name,
					},
				})
			}
		}
	}

	return res
}
