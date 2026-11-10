package kuik

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
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
// to finalizerMaxWait: what holds it, pods, a manage credential that cannot be read or a
// destination refusing deletion, rarely clears within seconds, and a pod event or a fix of the
// credential or of the spec reconciles it sooner anyway.
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

// finalize releases a deleted ImageMirror once no live pod runs one of its copies any more,
// with cleanup once it deleted every tag of this cluster in the repositories the mirror wrote
// to. The pods hold it whatever their imagePullPolicy: since Kubernetes 1.35 (KEP-2535) the
// kubelet checks, at every container start, that the pod's pull credentials match the ones that
// pulled the image, and the injected pull Secret goes with the mirror. A recreated pod goes
// through the webhook again, which offers no deleted mirror.
func (r *ImageMirrorReconciler) finalize(ctx context.Context, im *kuikv1alpha1.ImageMirror, resource routing.Resource) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(im, mirrorFinalizer) {
		return ctrl.Result{}, nil
	}
	// A copy writing after deleteTags read status.repositories would leave its tags behind: no
	// copy starts any more, and those running are waited for.
	st := r.state(im.Name)
	r.stop(im.Name, st)
	if n := st.halt(); n > 0 {
		return r.hold(ctx, im, resource, kuikv1alpha1.ReasonCopiesInFlight, inFlight(n), inFlightWait)
	}
	pods, err := r.runningCopies(ctx, im)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(pods) > 0 {
		r.announceWaiting(ctx, im, pods)
		return r.hold(ctx, im, resource, kuikv1alpha1.ReasonPodsRunningCopies, waitingCondition(pods), st.holdAgain())
	}
	st.waitedFor()
	if im.Spec.Cleanup.IsEnabled() {
		refused, unreadable, err := r.deleteTags(ctx, im)
		if err != nil {
			return ctrl.Result{}, err
		}
		if unreadable != nil {
			return r.hold(ctx, im, resource, kuikv1alpha1.ReasonManageCredentialUnreadable, manageUnreadable(unreadable), st.holdAgain())
		}
		if refused {
			enabled, err := r.reportRefused(ctx, im.Name, resource, im.Spec.Cleanup.DeletesByDigest())
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
			return ctrl.Result{RequeueAfter: st.holdAgain()}, nil
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

// refusedCleanup is why a deleted mirror whose destination refuses deletion, by digest or by
// tag, is not ready: only an operator can release it. A mirror deleting by tag is pointed to
// deleting by digest too.
func refusedCleanup(byDigest bool) condition.NotReady {
	const release = "set cleanup.enabled: false to release the ImageMirror and leave its tags in place"
	if byDigest {
		return condition.NotReady{
			Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
			Message: "the destination refuses manifest deletion: cleanup cannot make progress; " + release,
		}
	}
	return condition.NotReady{
		Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
		Message: "the destination refuses tag deletion: cleanup cannot make progress; " + release + ", or " + deleteByDigestHint,
	}
}

// reportRefused sets Ready False with reason RegistryDeleteUnsupported, and DeletionBlocked
// with reason DestinationRefusedDeletion, on a deleted mirror its destination holds, and writes
// the status only when that changed it: a held mirror is tried
// again for good. It reads the mirror again first, since deleting the tags took registry round
// trips, and reports false, writing nothing, when cleanup was disabled meanwhile: the next try
// releases it. byDigest is how the refused deletion was made, whatever the spec says now.
func (r *ImageMirrorReconciler) reportRefused(ctx context.Context, name string, resource routing.Resource, byDigest bool) (bool, error) {
	var im kuikv1alpha1.ImageMirror
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &im); err != nil {
		return false, err
	}
	if !im.Spec.Cleanup.IsEnabled() {
		return false, nil
	}
	next := im.Status.DeepCopy()
	notReady := refusedCleanup(byDigest)
	r.readiness.Set(&im, resource.Kind, resource.Name, &next.Conditions, im.Generation, &notReady)
	condition.SetAnomaly(&next.Conditions, kuikv1alpha1.ConditionDeletionBlocked, true,
		kuikv1alpha1.ReasonDestinationRefusedDeletion, notReady.Message, im.Generation)
	if equality.Semantic.DeepEqual(im.Status, *next) {
		return true, nil
	}
	im.Status = *next
	return true, r.Status().Update(ctx, &im)
}

// runningCopies returns the live pods with a container that runs an image under the destination
// of im, sorted by namespace and name.
func (r *ImageMirrorReconciler) runningCopies(ctx context.Context, im *kuikv1alpha1.ImageMirror) ([]*corev1.Pod, error) {
	var list corev1.PodList
	if err := r.List(ctx, &list); err != nil {
		return nil, err
	}
	var pods []*corev1.Pod
	for i := range list.Items {
		pod := &list.Items[i]
		if attribution.Live(pod) && runsCopy(pod, im.Spec.Destination.Path) {
			pods = append(pods, pod)
		}
	}
	slices.SortFunc(pods, func(a, b *corev1.Pod) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return pods, nil
}

// runsCopy reports whether a container of pod runs an image under destination.
func runsCopy(pod *corev1.Pod, destination string) bool {
	for _, c := range attribution.Containers(pod) {
		if ref, err := imagepath.Parse(c.Image); err == nil && mirrorpath.UnderDestination(destination, ref) {
			return true
		}
	}
	return false
}

// waitingSample is how many of the pods holding a deleted mirror its condition and its log
// line name.
const waitingSample = 5

// waiting says why n live pods running the copies of a deleted mirror hold it. It starts with n,
// which a restart reads back from DeletionBlocked.
func waiting(n int) string {
	pods := "live pods run"
	if n == 1 {
		pods = "live pod runs"
	}
	return fmt.Sprintf("%d %s copies of this ImageMirror: it and its injected pull Secrets are kept until they are gone, "+
		"since Kubernetes 1.35 a restarted container checks its pull Secret again; "+
		"recreate them (kubectl rollout restart) to release it", n, pods)
}

// waitingCondition is the message of DeletionBlocked while pods hold the mirror: waiting, then
// up to waitingSample of the pods.
func waitingCondition(pods []*corev1.Pod) string {
	sample := pods[:min(len(pods), waitingSample)]
	names := make([]string, len(sample))
	for i, pod := range sample {
		names[i] = pod.Namespace + "/" + pod.Name
	}
	message := waiting(len(pods)) + "; pods: " + strings.Join(names, ", ")
	if more := len(pods) - len(sample); more > 0 {
		message += fmt.Sprintf(" and %d more", more)
	}
	return message
}

// inFlight is the message of DeletionBlocked while n copies of the mirror still run.
func inFlight(n int) string {
	copies := "copies of this ImageMirror still run"
	if n == 1 {
		copies = "copy of this ImageMirror still runs"
	}
	return fmt.Sprintf("%d %s: it waits for them to end, within a copy timeout, so that none writes a tag after the cleanup", n, copies)
}

// manageUnreadable is the message of DeletionBlocked while err keeps the manage credential from
// being read.
func manageUnreadable(err error) string {
	return "the manage credential cannot be read: " + err.Error() +
		"; fix it, or set cleanup.enabled: false to release the ImageMirror and leave its tags in place"
}

// waitingCount returns how many pods the DeletionBlocked condition blocked says hold the mirror,
// 0 when it does not report such a hold.
func waitingCount(blocked *metav1.Condition) int {
	if blocked == nil || blocked.Status != metav1.ConditionTrue || blocked.Reason != kuikv1alpha1.ReasonPodsRunningCopies {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(blocked.Message, "%d ", &n); err != nil {
		return 0
	}
	return n
}

// waitingFor records that n pods hold the mirror, and reports whether that count changed. The
// count before the process's first hold is read from blocked, the persisted DeletionBlocked
// condition, so a restart announces no hold again.
func (st *mirrorState) waitingFor(n int, blocked *metav1.Condition) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.waitingSeeded {
		st.waitingSeeded = true
		st.waitingPods = waitingCount(blocked)
	}
	changed := st.waitingPods != n
	st.waitingPods = n
	return changed
}

// waitedFor records that no pod holds the mirror any more.
func (st *mirrorState) waitedFor() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.waitingSeeded = true
	st.waitingPods = 0
}

// announceWaiting emits WaitingForPods and logs up to waitingSample of the pods when the hold of
// the live pods running the copies of im starts or its count changes.
func (r *ImageMirrorReconciler) announceWaiting(ctx context.Context, im *kuikv1alpha1.ImageMirror, pods []*corev1.Pod) {
	blocked := meta.FindStatusCondition(im.Status.Conditions, kuikv1alpha1.ConditionDeletionBlocked)
	if !r.state(im.Name).waitingFor(len(pods), blocked) {
		return
	}
	sample := pods[:min(len(pods), waitingSample)]
	refs := make([]klog.ObjectRef, len(sample))
	for i, pod := range sample {
		refs[i] = klog.KObj(pod)
	}
	logf.FromContext(ctx).Info("Held a deleted ImageMirror: live pods run its copies", "count", len(pods), "pods", refs)
	r.recorder.Eventf(im, nil, corev1.EventTypeNormal, "WaitingForPods", "Delete", "%s", waiting(len(pods)))
}

// hold keeps the deleted mirror im for another try after wait, with DeletionBlocked True and
// reason, message; it writes the status only when that changed it.
func (r *ImageMirrorReconciler) hold(ctx context.Context, im *kuikv1alpha1.ImageMirror, resource routing.Resource, reason, message string, wait time.Duration) (ctrl.Result, error) {
	next := im.Status.DeepCopy()
	condition.SetAnomaly(&next.Conditions, kuikv1alpha1.ConditionDeletionBlocked, true, reason, message, im.Generation)
	if !equality.Semantic.DeepEqual(im.Status, *next) {
		im.Status = *next
		if err := r.Status().Update(ctx, im); err != nil {
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
	}
	return ctrl.Result{RequeueAfter: wait}, nil
}

// deleteTags deletes by tag, or by digest under cleanup.deleteByDigest, every tag of this
// cluster in the repositories of im. It got through when it reports neither that the
// destination refused a deletion as unsupported, nor unreadable, why the manage credential
// cannot be read. Either holds the mirror: released, it would take with it
// status.repositories, the only inventory of the tags left behind.
func (r *ImageMirrorReconciler) deleteTags(ctx context.Context, im *kuikv1alpha1.ImageMirror) (refused bool, unreadable error, err error) {
	if len(im.Status.Repositories) == 0 {
		return false, nil, nil
	}
	log := logf.FromContext(ctx)
	manage, err := r.manageAuth(ctx, im)
	var notFound *auth.ErrSecretNotFound
	var malformed *auth.ErrSecretMalformed
	if errors.As(err, &notFound) || errors.As(err, &malformed) {
		log.Info("Held a deleted ImageMirror: its manage credential cannot be read", "error", err.Error())
		return false, err, nil
	}
	if err != nil {
		return false, nil, err
	}
	endpoint := func(ref string) registry.Endpoint {
		return registry.Endpoint{Reference: ref, Insecure: im.Spec.Destination.Insecure, Auth: manage(ref)}
	}
	byDigest := im.Spec.Cleanup.DeletesByDigest()
	suffix := "_" + r.clusterID
	for _, repository := range im.Status.Repositories {
		tags, err := r.registry.ListTags(ctx, endpoint(repository))
		if err != nil {
			return false, nil, err
		}
		for _, tag := range tags {
			if !strings.HasSuffix(tag, suffix) {
				continue
			}
			ref := repository + ":" + tag
			if byDigest {
				// The mirror expects no tag any more: no guard holds the deletion.
				var outcome digestDeletion
				if outcome, err = r.deleteByDigest(ctx, endpoint, ref, nil, nil); err == nil && outcome.manifest != "" {
					log.Info("Deleted the manifest of a deleted ImageMirror", "image", outcome.manifest)
				}
			} else {
				err = r.registry.DeleteTag(ctx, endpoint(ref))
			}
			if deleteUnsupported(err) {
				log.Info("Held a deleted ImageMirror: its destination refuses deletion", "image", ref, "error", err.Error())
				r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "ImageDeletionFailed", "Delete", "Failed to delete %s: %v", ref, err)
				return true, nil, nil
			}
			if err != nil {
				return false, nil, err
			}
			log.Info("Deleted the tag of a deleted ImageMirror", "image", ref)
		}
	}
	return false, nil, nil
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
