package controllers

import (
	"context"
	_ "crypto/sha256"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"github.com/docker/distribution/reference"
	"github.com/enix/kube-image-keeper/api/v1alpha1"
	kuikenixiov1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const cachedImageOwnerKey = ".metadata.podOwner"
const LabelImageRewrittenName = "kuik.enix.io/images-rewritten"
const AnnotationOriginalImageTemplate = "original-image-%s"
const AnnotationOriginalInitImageTemplate = "original-init-image-%s"

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	ExpiryDelay time.Duration
}

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pod object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("reconciling pod")
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cachedImages := desiredCachedImages(ctx, &pod)

	finalizerName := "pod.kuik.enix.io/finalizer"

	// On pod deletion
	if !pod.DeletionTimestamp.IsZero() {
		if !containsString(pod.GetFinalizers(), finalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("pod is deleting")
		for _, cachedImage := range cachedImages {
			// Check if this CachedImage is in use by other pods
			var podsList corev1.PodList
			if err := r.List(ctx, &podsList, client.MatchingFields{cachedImageOwnerKey: cachedImage.Name}); err != nil {
				return ctrl.Result{}, err
			}
			cachedImageInUse := false
			for _, p := range podsList.Items {
				cachedImageInUse = cachedImageInUse || p.DeletionTimestamp.IsZero()
			}

			// Set an expiration date for unused CachedImage
			if !cachedImageInUse {
				expiresAt := metav1.NewTime(time.Now().Add(r.ExpiryDelay))
				log.Info("cachedimage not is use anymore, setting an expiry date", "cachedImage", klog.KObj(&cachedImage), "expiresAt", expiresAt)
				cachedImage.Spec.ExpiresAt = &expiresAt

				err := r.Patch(ctx, &cachedImage, client.Merge)
				if err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
			}

			var ci kuikenixiov1alpha1.CachedImage
			err := r.Get(ctx, client.ObjectKeyFromObject(&cachedImage), &ci)
			if err != nil && !apierrors.IsNotFound(err) {
				if apierrors.IsNotFound(err) {
					continue
				} else {
					return ctrl.Result{}, err
				}
			}

			err = r.updatePodCount(ctx, &ci)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		log.Info("removing finalizer")
		controllerutil.RemoveFinalizer(&pod, finalizerName)
		if err := r.Update(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("reconciled deleting pod")
		return ctrl.Result{}, nil
	}

	// Add finalizer to keep the Pod during expiring of CachedImages
	if !containsString(pod.GetFinalizers(), finalizerName) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(&pod, finalizerName)
		if err := r.Update(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}
	}

	// On pod creation and update
	for _, cachedImage := range cachedImages {
		var ci kuikenixiov1alpha1.CachedImage
		err := r.Get(ctx, client.ObjectKeyFromObject(&cachedImage), &ci)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !ci.DeletionTimestamp.IsZero() {
			// CachedImage is already scheduled for deletion, thus we don't have to handle it here and will enqueue it back later
			log.Info("cachedimage is already being deleted, skipping", "cachedImage", klog.KObj(&cachedImage))
			continue
		}

		// Create or update CachedImage depending on weather it already exists or not
		if apierrors.IsNotFound(err) {
			err = r.Create(ctx, &cachedImage)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			patch := client.MergeFrom(ci.DeepCopy())
			ci.Spec = cachedImage.Spec

			if err = r.Patch(ctx, &ci, patch); err != nil {
				return ctrl.Result{}, err
			}
			if err = r.updatePodCount(ctx, &ci); err != nil {
				return ctrl.Result{}, err
			}
		}

		log.Info("cachedimage patched", "cachedImage", klog.KObj(&cachedImage), "sourceImage", cachedImage.Spec.SourceImage)
	}

	log.Info("reconciled pod")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	p := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			_, ok := object.GetLabels()[LabelImageRewrittenName]
			return ok
		}))).
		Watches(
			&source.Kind{Type: &kuikenixiov1alpha1.CachedImage{}},
			handler.EnqueueRequestsFromMapFunc(r.podsWithDeletingCachedImages),
			builder.WithPredicates(p),
		).
		Complete(r)
}

func (r *PodReconciler) podsWithDeletingCachedImages(obj client.Object) []ctrl.Request {
	log := log.
		FromContext(context.Background()).
		WithName("controller-runtime.manager.controller.pod.deletingCachedImages").
		WithValues("cachedImage", klog.KObj(obj))

	var podList corev1.PodList
	podRequirements, _ := labels.NewRequirement(LabelImageRewrittenName, selection.Equals, []string{"true"})
	selector := labels.NewSelector()
	selector = selector.Add(*podRequirements)
	if err := r.List(context.Background(), &podList, &client.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		log.Error(err, "could not list pods")
		return nil
	}

	cachedImage := obj.(*kuikenixiov1alpha1.CachedImage)
	for _, pod := range podList.Items {
		for _, value := range pod.GetAnnotations() {
			// TODO check key format is "original-image-%s" or "original-init-image-%s"
			if cachedImage.Spec.SourceImage == value && !cachedImage.DeletionTimestamp.IsZero() {
				log.Info("image in use", "pod", pod.Namespace+"/"+pod.Name)
				res := make([]ctrl.Request, 1)
				res[0].Name = pod.Name
				res[0].Namespace = pod.Namespace
				return res
			}
		}
	}

	return make([]ctrl.Request, 0)
}

// updatePodCount update CachedImage UsedBy status
func (r *PodReconciler) updatePodCount(ctx context.Context, cachedImage *kuikenixiov1alpha1.CachedImage) error {
	var podsList corev1.PodList
	if err := r.List(ctx, &podsList, client.MatchingFields{cachedImageOwnerKey: cachedImage.Name}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	pods := []v1alpha1.PodReference{}
	for _, pod := range podsList.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		pods = append(pods, v1alpha1.PodReference{NamespacedName: pod.Namespace + "/" + pod.Name})
	}

	patch := client.MergeFrom(cachedImage.DeepCopy())
	cachedImage.Status.UsedBy = v1alpha1.UsedBy{
		Pods:  pods,
		Count: len(pods),
	}

	if err := r.Status().Patch(context.Background(), cachedImage, patch); err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

func desiredCachedImages(ctx context.Context, pod *corev1.Pod) []kuikenixiov1alpha1.CachedImage {
	pullSecretNames := []string{}

	for _, pullSecret := range pod.Spec.ImagePullSecrets {
		pullSecretNames = append(pullSecretNames, pullSecret.Name)
	}

	cachedImages := desiredCachedImagesForContainers(ctx, pod.Spec.Containers, pod.Annotations, AnnotationOriginalImageTemplate)
	cachedImages = append(cachedImages, desiredCachedImagesForContainers(ctx, pod.Spec.InitContainers, pod.Annotations, AnnotationOriginalInitImageTemplate)...)

	for i := range cachedImages {
		cachedImages[i].Spec.PullSecretNames = pullSecretNames
		cachedImages[i].Spec.PullSecretsNamespace = pod.Namespace
	}

	return cachedImages
}

func desiredCachedImagesForContainers(ctx context.Context, containers []corev1.Container, annotations map[string]string, annotationKeyTemplate string) []kuikenixiov1alpha1.CachedImage {
	log := log.FromContext(ctx)
	cachedImages := []kuikenixiov1alpha1.CachedImage{}

	for _, container := range containers {
		annotationKey := fmt.Sprintf(annotationKeyTemplate, container.Name)
		containerLog := log.WithValues("container", container.Name, "annotationKey", annotationKey)

		sourceImage, ok := annotations[annotationKey]
		if !ok {
			containerLog.V(1).Info("missing source image, ignoring: annotation not found")
			continue
		}

		cachedImage, err := cachedImageFromSourceImage(sourceImage)
		if err != nil {
			containerLog.Error(err, "could not create cached image, ignoring")
			continue
		}
		cachedImages = append(cachedImages, *cachedImage)

		containerLog.V(1).Info("desired CachedImage for container", "sourceImage", cachedImage.Spec.SourceImage)
	}

	return cachedImages
}

func cachedImageFromSourceImage(sourceImage string) (*kuikenixiov1alpha1.CachedImage, error) {
	ref, err := reference.ParseAnyReference(sourceImage)
	if err != nil {
		return nil, err
	}

	sanitizedName := registry.SanitizeName(ref.String())
	if !strings.Contains(sourceImage, ":") {
		sanitizedName += "-latest"
	}
	named, err := reference.ParseNormalizedNamed(ref.String())
	if err != nil {
		return nil, err
	}

	cachedImage := kuikenixiov1alpha1.CachedImage{
		TypeMeta: metav1.TypeMeta{APIVersion: kuikenixiov1alpha1.GroupVersion.String(), Kind: "CachedImage"},
		ObjectMeta: metav1.ObjectMeta{
			Name: sanitizedName,
			Labels: map[string]string{
				kuikenixiov1alpha1.RepositoryLabelName: registry.RepositoryLabel(named.Name()),
			},
		},
		Spec: kuikenixiov1alpha1.CachedImageSpec{
			SourceImage: sourceImage,
		},
	}

	return &cachedImage, nil
}
