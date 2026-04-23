package kuik

import (
	"context"
	"strings"

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

// ImageSetMirrorReconciler reconciles a ImageSetMirror object
type ImageSetMirrorReconciler struct {
	ImageSetMirrorBaseReconciler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *ImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var obj kuikv1alpha1.ImageSetMirror
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{Namespace: obj.Namespace}); err != nil {
		return ctrl.Result{}, err
	}

	return r.reconcileImageSetMirror(ctx, &obj, &obj.Spec, &obj.Status, false, pods.Items)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageSetMirror{}).
		Named("kuik-imagesetmirror").
		WithOptions(controller.Options{
			RateLimiter: newMirroringRateLimiter(),
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
				log := logf.FromContext(ctx).
					WithName("pod-mapper").
					WithValues("pod", klog.KObj(pod))

				var cisms kuikv1alpha1.ImageSetMirrorList
				if err := r.List(ctx, &cisms, &client.ListOptions{Namespace: pod.Namespace}); err != nil {
					log.Error(err, "failed to list ImageSetMirror")
					return nil
				}

				imageNames := normalizedImageNamesFromPod(pod)

				reqs := []reconcile.Request{}
				for _, cism := range cisms.Items {
					imageFilter := cism.Spec.ImageFilter.MustBuild()
					for imageName := range imageNames {
						if strings.Contains(imageName, "@") {
							continue // ignore digest-based images
						}

						if imageFilter.Match(imageName) {
							reqs = append(reqs, reconcile.Request{
								NamespacedName: client.ObjectKeyFromObject(&cism),
							})
							break
						}
					}
				}

				return reqs
			})),
		).
		Complete(r)
}
