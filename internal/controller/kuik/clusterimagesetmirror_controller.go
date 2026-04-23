package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ClusterImageSetMirrorReconciler reconciles a ClusterImageSetMirror object
type ClusterImageSetMirrorReconciler struct {
	ImageSetMirrorBaseReconciler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ClusterImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var obj kuikv1alpha1.ClusterImageSetMirror
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{Namespace: obj.Namespace}); err != nil {
		return ctrl.Result{}, err
	}

	spec := (*kuikv1alpha1.ImageSetMirrorSpec)(&obj.Spec)
	status := (*kuikv1alpha1.ImageSetMirrorStatus)(&obj.Status)
	return r.reconcileImageSetMirror(ctx, &obj, spec, status, true, pods.Items)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ClusterImageSetMirror{}).
		Named("kuik-clusterimagesetmirror").
		WithOptions(controller.Options{
			RateLimiter: newMirroringRateLimiter(),
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
				log := logf.FromContext(ctx).
					WithName("pod-mapper").
					WithValues("pod", klog.KObj(pod))

				var cisms kuikv1alpha1.ClusterImageSetMirrorList
				if err := r.List(ctx, &cisms); err != nil {
					log.Error(err, "failed to list ClusterImageSetMirror")
					return nil
				}

				imageNames := normalizedImageNamesFromPod(pod)

				reqs := []reconcile.Request{}
				for i := range cisms.Items {
					cism := &cisms.Items[i]
					if podImagesMatchFilter(imageNames, cism.Spec.ImageFilter.MustBuild()) {
						reqs = append(reqs, reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(cism),
						})
					}
				}

				return reqs
			})),
		).
		Complete(r)
}
