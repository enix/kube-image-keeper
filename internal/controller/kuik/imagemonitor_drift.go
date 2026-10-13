package kuik

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// drift returns the driftedImages entries of im: the tags pods declare whose running digests
// include one other than the digest the last check of the tag read upstream. See
// docs/v3/spec.md, "ImageMonitor". A digest-pinned reference never drifts, a retained one runs
// nowhere, and a tag no check answered yet has no upstream digest to compare to.
//
// The comparison is naive: a container runtime does not always report the digest of the
// manifest the tag points at in its imageID. containerd reports the digest of the index of a
// multi-arch image pulled by tag, which is what a HEAD of the tag reads, while other runtimes
// may report a platform manifest or a config digest, which never matches it. See
// https://github.com/kubernetes/kubernetes/issues/115199.
func (r *ImageMonitorReconciler) drift(ctx context.Context, im *kuikv1alpha1.ImageMonitor, memory *monitorMemory, declared map[string]declaredImage, upstream map[string]string, now time.Time) []kuikv1alpha1.MonitorDriftedImage {
	log := logf.FromContext(ctx)
	if !im.Spec.DetectsDrift() {
		if len(memory.drifts) > 0 {
			log.Info("Dropped the drifted tags of an ImageMonitor whose driftDetection is off", "count", len(memory.drifts))
		}
		memory.drifts = map[string]driftState{}
		return nil
	}
	stamp := metav1.NewTime(now).Rfc3339Copy().Time
	drifts := map[string]driftState{}
	var entries []kuikv1alpha1.MonitorDriftedImage
	for _, ref := range slices.Sorted(maps.Keys(declared)) {
		d := declared[ref]
		if d.undigested != nil && !memory.undigested[ref] && !pinned(ref) {
			// Its runtime reports a bare config digest: nothing to compare to the upstream one.
			memory.undigested[ref] = true
			log.Info("Found image running without a registry digest, its drift is not checked",
				"image", ref, "pod", klog.KObj(d.undigested))
		}
		digest, ok := upstream[ref]
		if !ok || pinned(ref) || !driftsFrom(d.runningDigests, digest) {
			continue
		}
		state, known := memory.drifts[ref]
		if !known {
			// A drift that goes on while the upstream moves again is the same drift: it is
			// announced once, when it starts.
			state = driftState{since: stamp}
			log.Info("Found image drifted from its upstream digest", "image", ref, "upstreamDigest", digest)
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, eventImageTagDrifted, "Check",
				"The upstream digest of %s moved to %s while pods run another one. Follow it or pin the digest", ref, digest)
		}
		skewed := len(d.runningDigests) > 1
		if skewed && !state.skewed {
			log.Info("Found image running several digests", "image", ref, "digests", len(d.runningDigests))
			r.recorder.Eventf(im, nil, corev1.EventTypeWarning, eventImageClusterSkew, "Check",
				"Pods run %s at %d different digests: part of the cluster has not caught up. "+
					"Restart the workloads or force a pull", ref, len(d.runningDigests))
		}
		if !skewed && state.skewed {
			log.Info("Found image running one digest again", "image", ref)
		}
		state.skewed = skewed
		drifts[ref] = state
		entries = append(entries, kuikv1alpha1.MonitorDriftedImage{
			Ref:            ref,
			UpstreamDigest: digest,
			Since:          metav1.NewTime(state.since),
			RunningDigests: runningDigests(d.runningDigests),
		})
	}
	for ref := range memory.drifts {
		if _, ok := drifts[ref]; !ok {
			// Its pods caught up, went away, were routed elsewhere or are still pulling.
			log.Info("Found image no longer drifted", "image", ref)
		}
	}
	memory.drifts = drifts
	slices.SortFunc(entries, func(a, b kuikv1alpha1.MonitorDriftedImage) int {
		return cmp.Or(a.Since.Compare(b.Since.Time), cmp.Compare(a.Ref, b.Ref))
	})
	return entries
}

// The events a drift emits, on the monitor. See docs/v3/observability.md, "Catalogue". The end
// of a drift emits nothing: the cluster caught up, which is not news.
const (
	eventImageTagDrifted  = "ImageTagDrifted"
	eventImageClusterSkew = "ImageClusterSkew"
)

// driftState is what a monitor remembers of a drifted tag.
type driftState struct {
	// since is when the drift was first observed, kept while it lasts.
	since time.Time
	// skewed is set while pods run the tag at more than one digest, once ImageClusterSkew
	// announced it.
	skewed bool
}

// skewOf returns, per drifted tag its pods run at several digests, how many.
func skewOf(entries []kuikv1alpha1.MonitorDriftedImage) map[string]int {
	skew := map[string]int{}
	for _, e := range entries {
		if len(e.RunningDigests) > 1 {
			skew[e.Ref] = len(e.RunningDigests)
		}
	}
	return skew
}

// pinned reports whether ref carries a digest: a digest cannot drift.
func pinned(ref string) bool {
	parsed, err := imagepath.Parse(ref)
	return err == nil && parsed.Digest != ""
}

// driftsFrom reports whether any of the running digests differs from upstream.
func driftsFrom(running map[string]int32, upstream string) bool {
	for digest := range running {
		if digest != upstream {
			return true
		}
	}
	return false
}

// runningDigests lists the digests running for a tag with their pod counts, by digest.
func runningDigests(running map[string]int32) []kuikv1alpha1.RunningDigest {
	out := make([]kuikv1alpha1.RunningDigest, 0, len(running))
	for _, digest := range slices.Sorted(maps.Keys(running)) {
		out = append(out, kuikv1alpha1.RunningDigest{Digest: digest, Pods: running[digest]})
	}
	return out
}

// refsOf returns the references of the drifted entries.
func refsOf(entries []kuikv1alpha1.MonitorDriftedImage) []string {
	refs := make([]string, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, e.Ref)
	}
	return refs
}
