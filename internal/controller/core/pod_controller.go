package core

import (
	"context"
	_ "crypto/sha256"
	"strings"

	"golang.org/x/exp/maps"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"

	"github.com/distribution/reference"
	kuikv1alpha1ext1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1ext1"
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
)

const CachedImageOwnerKey = ".metadata.podOwner"
const LabelManagedName = "kuik.enix.io/managed"
const AnnotationRewriteImagesName = "kuik.enix.io/rewrite-images"

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("reconciling pod")

	// On pod deletion
	if !pod.DeletionTimestamp.IsZero() {
		log.Info("pod is deleting")
		return ctrl.Result{}, nil
	}

	cachedImages := DesiredCachedImages(ctx, &pod)
	repositories, err := r.desiredRepositories(ctx, &pod, cachedImages)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// On pod creation and update
	for _, repository := range repositories {
		repo := repository.DeepCopy()

		operation, err := controllerutil.CreateOrPatch(ctx, r.Client, repo, func() error {
			repo.Spec.Name = repository.Spec.Name
			repo.Spec.PullSecretNames = repository.Spec.PullSecretNames
			repo.Spec.PullSecretsNamespace = repository.Spec.PullSecretsNamespace
			return nil
		})

		if err != nil {
			return ctrl.Result{}, err
		}

		log.Info("repository reconcilied", "repository", klog.KObj(&repository), "operation", operation)
	}

	for _, cachedImage := range cachedImages {
		var ci kuikv1alpha1ext1.CachedImage
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

			ci.Spec.SourceImage = cachedImage.Spec.SourceImage

			if err = r.Patch(ctx, &ci, patch); err != nil {
				return ctrl.Result{}, err
			}
		}

		log.Info("cachedimage patched", "cachedImage", klog.KObj(&cachedImage), "sourceImage", cachedImage.Spec.SourceImage)
	}

	log.Info("pod reconciled")
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
			_, ok := object.GetLabels()[LabelManagedName]
			return ok
		}))).
		Watches(
			&kuikv1alpha1ext1.CachedImage{},
			handler.EnqueueRequestsFromMapFunc(r.podsWithDeletingCachedImages),
			builder.WithPredicates(p),
		).
		Complete(r)
}

func (r *PodReconciler) podsWithDeletingCachedImages(ctx context.Context, obj client.Object) []ctrl.Request {
	log := log.
		FromContext(ctx).
		WithName("controller-runtime.manager.controller.pod.deletingCachedImages").
		WithValues("cachedImage", klog.KObj(obj))

	cachedImage := obj.(*kuikv1alpha1ext1.CachedImage)
	var currentCachedImage kuikv1alpha1ext1.CachedImage
	// wait for the CachedImage to be really deleted
	if err := r.Get(ctx, client.ObjectKeyFromObject(cachedImage), &currentCachedImage); err == nil || !apierrors.IsNotFound(err) {
		return make([]ctrl.Request, 0)
	}

	var podList corev1.PodList
	podRequirements, _ := labels.NewRequirement(LabelManagedName, selection.Equals, []string{"true"})
	selector := labels.NewSelector()
	selector = selector.Add(*podRequirements)
	if err := r.List(ctx, &podList, &client.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		log.Error(err, "could not list pods")
		return nil
	}

	for _, pod := range podList.Items {
		for _, value := range pod.GetAnnotations() {
			// TODO check key format is "original-image-%s" or "original-init-image-%s"
			if cachedImage.Spec.SourceImage == value {
				log.Info("image in use", "pod", pod.Namespace+"/"+pod.Name)
				res := make([]ctrl.Request, 1)
				res[0].NamespacedName = client.ObjectKeyFromObject(&pod)
				return res
			}
		}
	}

	return make([]ctrl.Request, 0)
}

func (r *PodReconciler) desiredRepositories(ctx context.Context, pod *corev1.Pod, cachedImages []kuikv1alpha1ext1.CachedImage) ([]kuikv1alpha1ext1.Repository, error) {
	repositories := map[string]kuikv1alpha1ext1.Repository{}

	pullSecretNames, err := r.imagePullSecretNamesFromPod(ctx, pod)
	if err != nil {
		return nil, err
	}

	for _, cachedImage := range cachedImages {
		named, err := cachedImage.Repository()
		if err != nil {
			return nil, err
		}
		repositoryName := named.Name()
		repositories[repositoryName] = kuikv1alpha1ext1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name: registry.SanitizeName(repositoryName),
			},
			Spec: kuikv1alpha1ext1.RepositorySpec{
				Name:                 repositoryName,
				PullSecretNames:      pullSecretNames,
				PullSecretsNamespace: pod.Namespace,
			},
		}
	}

	return maps.Values(repositories), nil
}

func DesiredCachedImages(ctx context.Context, pod *corev1.Pod) []kuikv1alpha1ext1.CachedImage {
	cachedImages := desiredCachedImagesForContainers(ctx, pod.Spec.Containers, pod.Annotations, false)
	cachedImages = append(cachedImages, desiredCachedImagesForContainers(ctx, pod.Spec.InitContainers, pod.Annotations, true)...)
	return cachedImages
}

func desiredCachedImagesForContainers(ctx context.Context, containers []corev1.Container, annotations map[string]string, initContainer bool) []kuikv1alpha1ext1.CachedImage {
	log := log.FromContext(ctx)
	cachedImages := []kuikv1alpha1ext1.CachedImage{}

	for _, container := range containers {
		annotationKey := registry.ContainerAnnotationKey(container.Name, initContainer)
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

func cachedImageFromSourceImage(sourceImage string) (*kuikv1alpha1ext1.CachedImage, error) {
	ref, err := reference.ParseAnyReference(sourceImage)
	if err != nil {
		return nil, err
	}

	sanitizedName := registry.SanitizeName(ref.String())
	if !strings.Contains(sourceImage, ":") {
		sanitizedName += "-latest"
	}

	cachedImage := kuikv1alpha1ext1.CachedImage{
		TypeMeta: metav1.TypeMeta{APIVersion: kuikv1alpha1ext1.GroupVersion.String(), Kind: "CachedImage"},
		ObjectMeta: metav1.ObjectMeta{
			Name: sanitizedName,
		},
		Spec: kuikv1alpha1ext1.CachedImageSpec{
			SourceImage: sourceImage,
		},
	}

	return &cachedImage, nil
}

func (r *PodReconciler) imagePullSecretNamesFromPod(ctx context.Context, pod *corev1.Pod) ([]string, error) {
	if pod.Spec.ServiceAccountName == "" {
		return []string{}, nil
	}

	var serviceAccount corev1.ServiceAccount
	serviceAccountNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Spec.ServiceAccountName}
	if err := r.Get(ctx, serviceAccountNamespacedName, &serviceAccount); err != nil && !apierrors.IsNotFound(err) {
		return []string{}, err
	}

	imagePullSecrets := append(pod.Spec.ImagePullSecrets, serviceAccount.ImagePullSecrets...)
	imagePullSecretNames := make([]string, len(imagePullSecrets))

	for i, imagePullSecret := range imagePullSecrets {
		imagePullSecretNames[i] = imagePullSecret.Name
	}

	return imagePullSecretNames, nil
}
