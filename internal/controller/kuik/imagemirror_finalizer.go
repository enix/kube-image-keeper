package kuik

import (
	"context"
	"errors"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
	"github.com/enix/kube-image-keeper/internal/status/condition"
)

// mirrorFinalizer holds a deleted ImageMirror until its tags are gone: status.repositories,
// the only inventory of the repositories it wrote to, goes with the object.
const mirrorFinalizer = "kuik.enix.io/mirror-cleanup"

// A deleted mirror its finalizer holds tries again after finalizerWait, doubled at each try up
// to finalizerMaxWait: what holds it, pods or a destination refusing deletion, rarely clears
// within seconds, and a pod event reconciles it sooner anyway.
const (
	finalizerWait    = 10 * time.Second
	finalizerMaxWait = 30 * time.Minute
)

// inFlightWait is how long a deleted mirror waits for the copies still running: one ends
// within a copy timeout, and wakes the mirror.
const inFlightWait = time.Second

// halt lets no copy of the mirror start any more, and returns how many still run.
func (st *mirrorState) halt() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.stopping = true
	return st.inFlight
}

// holdAgain returns how long a held mirror waits before its next try, and counts the try.
func (st *mirrorState) holdAgain() time.Duration {
	st.mu.Lock()
	defer st.mu.Unlock()
	wait := finalizerMaxWait
	if st.held < 8 {
		wait = min(finalizerWait<<st.held, finalizerMaxWait)
	}
	st.held++
	return wait
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors,verbs=update;patch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors/finalizers,verbs=update,roleName=reconciler

// finalize releases a deleted ImageMirror once no pod runs one of its copies any more: a node
// rescheduling such a pod would find its image gone. With cleanup, it first deletes by tag
// every tag of this cluster in the repositories the mirror wrote to.
func (r *ImageMirrorReconciler) finalize(ctx context.Context, im *kuikv1alpha1.ImageMirror, resource routing.Resource) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(im, mirrorFinalizer) {
		return ctrl.Result{}, nil
	}
	// A copy writing after deleteTags read status.repositories would leave its tags behind: no
	// copy starts any more, and those running are waited for.
	st := r.state(im.Name)
	r.stop(im.Name, st)
	if st.halt() > 0 {
		return ctrl.Result{RequeueAfter: inFlightWait}, nil
	}
	running, err := r.runsCopies(ctx, im)
	if err != nil {
		return ctrl.Result{}, err
	}
	if running {
		return ctrl.Result{RequeueAfter: r.state(im.Name).holdAgain()}, nil
	}
	if im.Spec.Cleanup.IsEnabled() {
		done, refused, err := r.deleteTags(ctx, im)
		if err != nil {
			return ctrl.Result{}, err
		}
		if refused {
			enabled, err := r.reportRefused(ctx, im.Name, resource)
			if err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{RequeueAfter: conflictWait}, nil
				}
				// Its finalizer was removed meanwhile: the mirror is gone, as if released.
				if apierrors.IsNotFound(err) {
					r.forget(resource)
					return ctrl.Result{}, nil
				}
				return ctrl.Result{}, err
			}
			if !enabled {
				// Cleanup was disabled meanwhile: the next try releases the mirror at once.
				return ctrl.Result{RequeueAfter: conflictWait}, nil
			}
		}
		if !done {
			return ctrl.Result{RequeueAfter: r.state(im.Name).holdAgain()}, nil
		}
	}
	original := im.DeepCopy()
	controllerutil.RemoveFinalizer(im, mirrorFinalizer)
	if err := r.patchFinalizers(ctx, im, original); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: conflictWait}, nil
		}
		return ctrl.Result{}, err
	}
	r.forget(resource)
	logf.FromContext(ctx).Info("Released deleted ImageMirror")
	return ctrl.Result{}, nil
}

// patchFinalizers writes the finalizers of im, changed from original, and nothing else. An
// Update would send the spec back as Go serialises it (a defaulted retention of 168h becomes
// 168h0m0s), bump the generation and show GitOps tools a drift. The resourceVersion guards
// the list a merge patch replaces whole against a finalizer added meanwhile.
func (r *ImageMirrorReconciler) patchFinalizers(ctx context.Context, im, original *kuikv1alpha1.ImageMirror) error {
	return r.Patch(ctx, im, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}

// refusedCleanup is why a deleted mirror whose destination refuses tag deletion is not ready:
// only an operator can release it.
var refusedCleanup = condition.NotReady{
	Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
	Message: "the destination refuses tag deletion: cleanup cannot make progress; set cleanup.enabled: false to release the ImageMirror and leave its tags in place",
}

// reportRefused sets Ready False with reason RegistryDeleteUnsupported on a deleted mirror its
// destination holds, and writes the status only when that changed it: a held mirror is tried
// again for good. It reads the mirror again first, since deleting the tags took registry round
// trips, and reports false, writing nothing, when cleanup was disabled meanwhile: the next try
// releases it.
func (r *ImageMirrorReconciler) reportRefused(ctx context.Context, name string, resource routing.Resource) (bool, error) {
	var im kuikv1alpha1.ImageMirror
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &im); err != nil {
		return false, err
	}
	if !im.Spec.Cleanup.IsEnabled() {
		return false, nil
	}
	next := im.Status.DeepCopy()
	notReady := refusedCleanup
	r.readiness.Set(&im, resource.Kind, resource.Name, &next.Conditions, im.Generation, &notReady)
	if equality.Semantic.DeepEqual(im.Status, *next) {
		return true, nil
	}
	im.Status = *next
	return true, r.Status().Update(ctx, &im)
}

// runsCopies reports whether a live pod runs an image under the destination of im.
func (r *ImageMirrorReconciler) runsCopies(ctx context.Context, im *kuikv1alpha1.ImageMirror) (bool, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !attribution.Live(pod) {
			continue
		}
		for _, c := range attribution.Containers(pod) {
			if ref, err := imagepath.Parse(c.Image); err == nil && mirrorpath.UnderDestination(im.Spec.Destination.Path, ref) {
				return true, nil
			}
		}
	}
	return false, nil
}

// deleteTags deletes by tag every tag of this cluster in the repositories of im, and reports
// whether it got through, and whether the destination refused a deletion as unsupported. A
// manage credential that cannot be read or a destination refusing tag deletion holds the
// mirror: released, it would take with it status.repositories, the only inventory of the tags
// left behind.
func (r *ImageMirrorReconciler) deleteTags(ctx context.Context, im *kuikv1alpha1.ImageMirror) (bool, bool, error) {
	if len(im.Status.Repositories) == 0 {
		return true, false, nil
	}
	log := logf.FromContext(ctx)
	manage, err := r.manageAuth(ctx, im)
	var notFound *auth.ErrSecretNotFound
	var malformed *auth.ErrSecretMalformed
	if errors.As(err, &notFound) || errors.As(err, &malformed) {
		log.Info("Held a deleted ImageMirror: its manage credential cannot be read", "error", err.Error())
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	suffix := "_" + r.clusterID
	for _, repository := range im.Status.Repositories {
		endpoint := registry.Endpoint{Reference: repository, Insecure: im.Spec.Destination.Insecure, Auth: manage(repository)}
		tags, err := r.registry.ListTags(ctx, endpoint)
		if err != nil {
			return false, false, err
		}
		for _, tag := range tags {
			if !strings.HasSuffix(tag, suffix) {
				continue
			}
			endpoint.Reference = repository + ":" + tag
			err := r.registry.DeleteTag(ctx, endpoint)
			if errors.Is(err, registry.ErrDeleteUnsupported) {
				log.Info("Held a deleted ImageMirror: its destination refuses tag deletion", "image", endpoint.Reference, "error", err.Error())
				r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "ImageDeletionFailed", "Delete", "Failed to delete %s: %v", endpoint.Reference, err)
				return false, true, nil
			}
			if err != nil {
				return false, false, err
			}
			log.Info("Deleted the tag of a deleted ImageMirror", "image", endpoint.Reference)
		}
	}
	return true, false, nil
}

// drop forgets everything a process holds for the mirror name: its copy queues, its drift
// rings and its memory.
func (r *ImageMirrorReconciler) drop(name string) {
	r.statesMu.Lock()
	st, ok := r.states[name]
	delete(r.states, name)
	r.statesMu.Unlock()
	if ok {
		r.stop(name, st)
	}
}

// stop removes the copy queues, the drift rings and the destination scan of the mirror name,
// and keeps its memory.
func (r *ImageMirrorReconciler) stop(name string, st *mirrorState) {
	owner := pacing.Owner{Kind: routing.KindImageMirror, Name: name}
	r.scheduler.RemoveDestinationScan(owner)
	st.mu.Lock()
	queues, rings := st.queues, st.rings
	st.queues, st.rings = map[string]bool{}, map[string]bool{}
	st.mu.Unlock()
	for host := range queues {
		r.scheduler.SetCopyQueue(owner, host, nil, mirrorCopier{r: r, name: name, host: host})
	}
	for host := range rings {
		r.scheduler.RemoveRing(owner, host)
	}
}
