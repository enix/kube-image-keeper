package kuik

import (
	"cmp"
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/routing"
)

// turnRings hands the scheduler, under driftPolicy Warn or Sync, one ring per source host of
// the copied tags of im to re-read there, resuming each from the cursor its status persisted.
// A digest cannot move, so a pinned reference is never re-read. Under Ignore it drops them.
func (r *ImageMirrorReconciler) turnRings(im *kuikv1alpha1.ImageMirror, st *mirrorState, desired []imagepath.Reference) {
	owner := pacing.Owner{Kind: routing.KindImageMirror, Name: im.Name}
	policy := im.Spec.DriftPolicy
	rings := map[string][]string{}

	st.mu.Lock()
	st.policy = policy
	if policy == kuikv1alpha1.DriftPolicyWarn || policy == kuikv1alpha1.DriftPolicySync {
		for _, ref := range desired {
			key := ref.String()
			if ref.Tag == "" || ref.Digest != "" || st.digests[key] == "" {
				continue
			}
			rings[ref.Host] = append(rings[ref.Host], key)
		}
	}
	tracked := map[string]bool{}
	for _, refs := range rings {
		for _, ref := range refs {
			tracked[ref] = true
		}
	}
	for ref := range st.drifted {
		if !tracked[ref] {
			delete(st.drifted, ref)
			delete(st.resync, ref)
		}
	}
	gone := []string{}
	for host := range st.rings {
		if _, ok := rings[host]; !ok {
			gone = append(gone, host)
		}
	}
	st.rings = map[string]bool{}
	for host := range rings {
		st.rings[host] = true
	}
	st.mu.Unlock()

	resume := map[string]kuikv1alpha1.RegistryCheck{}
	if im.Status.Checks != nil {
		for _, ring := range im.Status.Checks.Registries {
			resume[ring.Registry] = ring
		}
	}
	checker := driftChecker{r: r, name: im.Name}
	for host, refs := range rings {
		r.scheduler.SetRing(owner, host, refs, resume[host], checker)
	}
	for _, host := range gone {
		r.scheduler.RemoveRing(owner, host)
	}
}

// driftChecker re-reads the upstream tags one ImageMirror copied.
type driftChecker struct {
	r    *ImageMirrorReconciler
	name string
}

// Check reads the upstream tag ref with the credentials a node would pull it with.
func (c driftChecker) Check(ctx context.Context, ref string) pacing.Response {
	st := c.r.state(c.name)
	st.mu.Lock()
	pod := st.declaring[ref]
	st.mu.Unlock()
	endpoint, err := c.r.sourceEndpoint(ctx, routing.Candidate{Reference: ref}, pod)
	if err != nil {
		return pacing.Response{Err: err}
	}
	result, err := c.r.registry.Check(ctx, endpoint)
	return pacing.Response{Result: result, Err: err}
}

// Checked compares the upstream digest of ref with the copy: a moved one is drift, which Sync
// copies again. A failed read gives no verdict.
func (c driftChecker) Checked(ref string, response pacing.Response) {
	if response.Err != nil || response.Result == nil {
		return
	}
	st := c.r.state(c.name)
	st.mu.Lock()
	copied := st.digests[ref]
	upstream := response.Result.Descriptor.Digest.String()
	switch {
	case copied == "":
	case upstream == copied:
		delete(st.drifted, ref)
		delete(st.resync, ref)
	default:
		if previous, ok := st.drifted[ref]; !ok || previous.upstream != upstream {
			st.drifted[ref] = driftEntry{upstream: upstream, copied: copied, at: c.r.clock.Now()}
		}
		if st.policy == kuikv1alpha1.DriftPolicySync {
			st.resync[ref] = true
		}
	}
	st.mu.Unlock()
	c.r.wake(c.name)
}

// reportDrift returns the driftedImages of im, since kept from the previous status, and emits
// CopyOutOfDate under Warn for each tag newly found drifted.
func (r *ImageMirrorReconciler) reportDrift(im *kuikv1alpha1.ImageMirror, st *mirrorState) []kuikv1alpha1.MirrorDriftedImage {
	st.mu.Lock()
	defer st.mu.Unlock()
	previous := map[string]kuikv1alpha1.MirrorDriftedImage{}
	for _, e := range im.Status.DriftedImages {
		previous[e.Ref] = e
	}
	entries := make([]kuikv1alpha1.MirrorDriftedImage, 0, len(st.drifted))
	for ref, drift := range st.drifted {
		entry := kuikv1alpha1.MirrorDriftedImage{Ref: ref, UpstreamDigest: drift.upstream, CopiedDigest: drift.copied, Since: metav1.NewTime(drift.at)}
		if p, ok := previous[ref]; ok {
			entry.Since = p.Since
		} else if im.Spec.DriftPolicy == kuikv1alpha1.DriftPolicyWarn && !drift.announced {
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, "CopyOutOfDate", "Check",
				"The upstream tag %s moved to %s, the mirror keeps serving %s", ref, drift.upstream, drift.copied)
		}
		drift.announced = true
		st.drifted[ref] = drift
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b kuikv1alpha1.MirrorDriftedImage) int {
		return cmp.Or(a.Since.Compare(b.Since.Time), cmp.Compare(a.Ref, b.Ref))
	})
	return entries
}

// checks is the status of the drift rings of im, nil when it holds none.
func (r *ImageMirrorReconciler) checks(im *kuikv1alpha1.ImageMirror) *kuikv1alpha1.ChecksStatus {
	registries := r.scheduler.RegistryChecks(pacing.Owner{Kind: routing.KindImageMirror, Name: im.Name})
	if len(registries) == 0 {
		return nil
	}
	return &kuikv1alpha1.ChecksStatus{Registries: registries}
}
