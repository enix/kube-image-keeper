package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClusterImageSetMirrorReconciler reconciles a ClusterImageSetMirror object.
// All reconciliation logic lives in ImageSetMirrorBaseReconciler; this type only
// supplies the concrete object kind and the cluster-wide listing of
// ClusterImageSetMirror resources.
type ClusterImageSetMirrorReconciler struct {
	ImageSetMirrorBaseReconciler
}

func (r *ClusterImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconcile(ctx, req, &kuikv1alpha1.ClusterImageSetMirror{})
}

// mapPodToRequests enqueues every ClusterImageSetMirror.
func (r *ClusterImageSetMirrorReconciler) mapPodToRequests(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
	return r.enqueueForPod(ctx, pod, func(ctx context.Context) ([]MirrorObject, error) {
		var list kuikv1alpha1.ClusterImageSetMirrorList
		if err := r.List(ctx, &list); err != nil {
			return nil, err
		}
		objs := make([]MirrorObject, len(list.Items))
		for i := range list.Items {
			objs[i] = &list.Items[i]
		}
		return objs, nil
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.setupController(mgr, "kuik-clusterimagesetmirror", &kuikv1alpha1.ClusterImageSetMirror{}, r.mapPodToRequests, r)
}
