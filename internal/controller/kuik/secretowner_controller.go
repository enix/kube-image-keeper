package kuik

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	cleanupFinalizer = "kuik.enix.io/secret-cleanup"
	OwnerUIDLabel    = "kuik.enix.io/owner-uid"
)

// SecretOwnerReconciler reconciles any object that owns one or more Secret
type SecretOwnerReconciler[T client.Object] struct {
	client.Client
	Scheme *runtime.Scheme
	New    func() T
}

func (s *SecretOwnerReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	owner := s.New()
	if err := s.Get(ctx, req.NamespacedName, owner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !owner.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(owner, cleanupFinalizer) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, s.CleanupOwnedSecrets(ctx, owner)
	}

	if !controllerutil.ContainsFinalizer(owner, cleanupFinalizer) {
		log.Info("adding finalizer")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := s.Get(ctx, client.ObjectKeyFromObject(owner), owner); err != nil {
				return err
			}

			controllerutil.AddFinalizer(owner, cleanupFinalizer)
			return s.Update(ctx, owner)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (s *SecretOwnerReconciler[T]) SetupWithManager(mgr ctrl.Manager) error {
	gvk, _ := apiutil.GVKForObject(s.New(), s.Scheme)
	return ctrl.NewControllerManagedBy(mgr).
		For(s.New()).
		Named("kuik-secretowner-" + gvk.Kind).
		Complete(s)
}

func (s *SecretOwnerReconciler[T]) CleanupOwnedSecrets(ctx context.Context, owner client.Object) error {
	log := logf.FromContext(ctx)
	uid := string(owner.GetUID())

	var secrets corev1.SecretList
	if err := s.List(ctx, &secrets,
		client.MatchingLabels{
			OwnerUIDLabel: uid,
		},
	); err != nil {
		return err
	}

	for i := range secrets.Items {
		log.V(1).Info("cleaning up secret", "secret", klog.KObj(&secrets.Items[i]))
		if err := s.Delete(ctx, &secrets.Items[i]); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	log.Info("removing finalizer")
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := s.Get(ctx, client.ObjectKeyFromObject(owner), owner); err != nil {
			return client.IgnoreNotFound(err)
		}

		controllerutil.RemoveFinalizer(owner, cleanupFinalizer)
		return s.Update(ctx, owner)
	})
}
