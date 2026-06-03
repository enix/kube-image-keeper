package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ImageSetMirrorReconciler reconciles a ImageSetMirror object.
// All reconciliation logic lives in ImageSetMirrorBaseReconciler; this type only
// supplies the concrete object kind and the listing of ImageSetMirror resources.
type ImageSetMirrorReconciler struct {
	ImageSetMirrorBaseReconciler
}

func (r *ImageSetMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconcile(ctx, req, &kuikv1alpha1.ImageSetMirror{})
}

// mapPodToRequests enqueues ImageSetMirrors in the pod's own namespace.
func (r *ImageSetMirrorReconciler) mapPodToRequests(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
	return r.enqueueForPod(ctx, pod, func(ctx context.Context) ([]MirrorObject, error) {
		var list kuikv1alpha1.ImageSetMirrorList
		if err := r.List(ctx, &list, &client.ListOptions{Namespace: pod.Namespace}); err != nil {
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
func (r *ImageSetMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.setupController(mgr, "kuik-imagesetmirror", &kuikv1alpha1.ImageSetMirror{}, r.mapPodToRequests, r)
}
