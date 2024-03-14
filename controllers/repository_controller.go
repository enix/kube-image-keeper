package controllers

import (
	"context"
	"regexp"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
)

const (
	repositoryFinalizerName = "repository.kuik.enix.io/finalizer"
	typeReadyRepository     = "Ready"
)

// RepositoryReconciler reconciles a Repository object
type RepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kuik.enix.io,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kuik.enix.io,resources=repositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kuik.enix.io,resources=repositories/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Repository object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var repository kuikv1alpha1.Repository
	if err := r.Get(ctx, req.NamespacedName, &repository); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("reconciling repository")

	var cachedImageList kuikv1alpha1.CachedImageList
	if err := r.List(ctx, &cachedImageList, client.MatchingFields{repositoryOwnerKey: repository.Name}); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	repository.Status.Images = len(cachedImageList.Items)

	if !repository.ObjectMeta.DeletionTimestamp.IsZero() {
		r.UpdateStatus(ctx, &repository, []metav1.Condition{{
			Type:    typeReadyRepository,
			Status:  metav1.ConditionFalse,
			Reason:  "AskedForDeletion",
			Message: "Repository has been asked to be deleted",
		}})

		if controllerutil.ContainsFinalizer(&repository, repositoryFinalizerName) {

			log.Info("repository is deleting", "cachedImages", len(cachedImageList.Items))
			if len(cachedImageList.Items) > 0 {
				return ctrl.Result{}, nil
			}

			log.Info("removing finalizer")
			controllerutil.RemoveFinalizer(&repository, repositoryFinalizerName)
			if err := r.Update(ctx, &repository); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if repository.Spec.UpdateInterval != nil {
		nextUpdate := repository.Status.LastUpdate.Add(repository.Spec.UpdateInterval.Duration)
		if time.Now().After(nextUpdate) {
			log.Info("updating repository")
			if err := r.List(ctx, &cachedImageList, client.MatchingFields{repositoryOwnerKey: repository.Name}); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}

			regexps, err := repository.CompileUpdateFilters()
			if err != nil {
				return ctrl.Result{}, err
			}

			for _, cachedImage := range cachedImageList.Items {
				if !isImageFilteredForUpdate(cachedImage.Spec.SourceImage, regexps) {
					continue
				}
				patch := client.MergeFrom(cachedImage.DeepCopy())
				if cachedImage.Annotations == nil {
					cachedImage.Annotations = map[string]string{}
				}
				cachedImage.Annotations[cachedImageAnnotationForceUpdateName] = "true"
				r.Patch(ctx, &cachedImage, patch)
			}

			repository.Status.LastUpdate = metav1.NewTime(time.Now())
		}
	}

	err := r.UpdateStatus(ctx, &repository, []metav1.Condition{{
		Type:    typeReadyRepository,
		Status:  metav1.ConditionTrue,
		Reason:  "Created",
		Message: "Repository is ready",
	}})
	if err != nil {
		return ctrl.Result{}, err
	}

	// Add finalizer to keep the Repository during image removal from registry on deletion
	if !controllerutil.ContainsFinalizer(&repository, repositoryFinalizerName) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(&repository, repositoryFinalizerName)
		if err := r.Update(ctx, &repository); err != nil {
			return ctrl.Result{}, err
		}
	}

	if repository.Spec.UpdateInterval != nil {
		return ctrl.Result{RequeueAfter: repository.Spec.UpdateInterval.Duration}, nil
	}

	return ctrl.Result{}, nil
}

func (r *RepositoryReconciler) UpdateStatus(ctx context.Context, repository *kuikv1alpha1.Repository, conditions []metav1.Condition) error {
	log := log.FromContext(ctx)

	for _, condition := range conditions {
		meta.SetStatusCondition(&repository.Status.Conditions, condition)
	}

	conditionReady := meta.FindStatusCondition(repository.Status.Conditions, typeReadyRepository)
	if conditionReady.Status == metav1.ConditionTrue {
		repository.Status.Phase = "Ready"
	} else if conditionReady.Status == metav1.ConditionFalse {
		repository.Status.Phase = "Terminating"
	} else {
		repository.Status.Phase = ""
	}

	if err := r.Status().Update(ctx, repository); err != nil {
		log.Error(err, "Failed to update Repository status")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	p := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}

	// Create an index to list CachedImage by Repository
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kuikv1alpha1.CachedImage{}, repositoryOwnerKey, func(rawObj client.Object) []string {
		cachedImage := rawObj.(*kuikv1alpha1.CachedImage)

		owners := cachedImage.GetOwnerReferences()
		for _, owner := range owners {
			if owner.APIVersion != kuikv1alpha1.GroupVersion.String() || owner.Kind != "Repository" {
				return nil
			}

			return []string{owner.Name}
		}

		return []string{}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.Repository{}).
		Watches(
			&source.Kind{Type: &kuikv1alpha1.CachedImage{}},
			handler.EnqueueRequestsFromMapFunc(r.repositoryWithDeletingCachedImages),
			builder.WithPredicates(p),
		).
		Watches(
			&source.Kind{Type: &kuikv1alpha1.CachedImage{}},
			handler.EnqueueRequestsFromMapFunc(requestRepositoryFromCachedImage),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return true
				},
			}),
		).
		Complete(r)
}

func (r *RepositoryReconciler) repositoryWithDeletingCachedImages(obj client.Object) []ctrl.Request {
	cachedImage := obj.(*kuikv1alpha1.CachedImage)
	var currentCachedImage kuikv1alpha1.CachedImage
	// wait for the CachedImage to be really deleted
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cachedImage), &currentCachedImage); err == nil || !apierrors.IsNotFound(err) {
		return nil
	}

	return requestRepositoryFromCachedImage(cachedImage)
}

func requestRepositoryFromCachedImage(obj client.Object) []ctrl.Request {
	cachedImage := obj.(*kuikv1alpha1.CachedImage)
	repositoryName, ok := cachedImage.Labels[kuikv1alpha1.RepositoryLabelName]
	if !ok {
		return nil
	}

	return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: repositoryName}}}
}

func isImageFilteredForUpdate(imageName string, regexps []regexp.Regexp) bool {
	if len(regexps) == 0 {
		return true
	}

	for _, regexp := range regexps {
		if regexp.Match([]byte(imageName)) {
			return true
		}
	}

	return false
}
