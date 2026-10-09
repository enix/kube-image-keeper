package plan

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
)

// Observed is what a mirror's status counts are computed from.
type Observed struct {
	// ClusterID and Path render the destination reference of a reference.
	ClusterID string
	Path      string
	// Live are the references the selected pods declare.
	Live []imagepath.Reference
	// Images are the live images of the containers of the selected pods, normalised.
	Images []string
	// SelfChecked are the references the last self-check found at the destination, keyed by
	// their String form.
	SelfChecked map[string]bool
	// Copied are the references copied since the last self-check, keyed the same way.
	Copied map[string]bool
	// Drifted are the drifted references and Failed the reason of each failing copy, as the
	// reconciler remembers them, keyed the same way: the status lists are a capped sample, so
	// they count along with them.
	Drifted map[string]bool
	Failed  map[string]kuikv1alpha1.CopyFailureReason
	// Status is the mirror's status: pendingDeletion, failedImageCopies and driftedImages.
	Status kuikv1alpha1.ImageMirrorStatus
}

// Counts computes status.images.copy. See docs/v3/status.md, "ImageMirror".
func Counts(o Observed) kuikv1alpha1.CopyCounts {
	var c kuikv1alpha1.CopyCounts
	images := toSet(o.Images)
	tracked := map[string]bool{}

	for _, ref := range o.Live {
		tracked[ref.String()] = true
		// A not yet copied reference is never routed to, so it is standby rather than running.
		if images[mirrorpath.Destination(o.Path, ref, o.ClusterID)] {
			c.Running++
		} else {
			c.Standby++
		}
	}
	// A pinned reference leaves two tags in pendingDeletion, its tag and its anchor, and
	// tracked counts references: retained counts origins, orphanTags counts tags.
	for _, entry := range o.Status.PendingDeletion {
		switch {
		case entry.Origin == "":
			c.OrphanTags++
		case !tracked[entry.Origin]:
			tracked[entry.Origin] = true
			c.Retained++
		}
	}
	c.Tracked = c.Running + c.Standby + c.Retained

	for ref := range tracked {
		if o.SelfChecked[ref] || o.Copied[ref] {
			c.Available++
		}
	}
	c.Unavailable = c.Tracked - c.Available

	drifted := maps.Clone(o.Drifted)
	if drifted == nil {
		drifted = map[string]bool{}
	}
	for _, entry := range o.Status.DriftedImages {
		drifted[entry.Ref] = true
	}
	missing := map[string]bool{}
	for ref, reason := range o.Failed {
		if reason == kuikv1alpha1.CopySourceNotFound {
			missing[ref] = true
		}
	}
	for _, failed := range o.Status.FailedImageCopies {
		if failed.Reason == kuikv1alpha1.CopySourceNotFound {
			missing[failed.Ref] = true
		}
	}
	c.Drifted = int32(len(drifted))
	c.MissingSource = int32(len(missing))
	return c
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// CopyFailureEvent is one ImageCopyFailed event: a reason and the message naming what failed.
type CopyFailureEvent struct {
	Reason  kuikv1alpha1.CopyFailureReason
	Message string
}

// CopyFailureEvents coalesces the copy failures of one pass: one event per reason and host,
// naming a single image, or the count of images under their narrowest common prefix. The
// prefix names the images that failed, never a scope: nothing is said about the others. See
// docs/v3/observability.md, "A copy failure is coalesced, never generalised".
func CopyFailureEvents(failed []kuikv1alpha1.FailedImageCopy) []CopyFailureEvent {
	type group struct {
		reason kuikv1alpha1.CopyFailureReason
		host   string
	}
	groups := map[group][]string{}
	for _, f := range failed {
		host := f.Ref
		if ref, err := imagepath.Parse(f.Ref); err == nil {
			host = ref.Host
		}
		g := group{f.Reason, host}
		groups[g] = append(groups[g], f.Ref)
	}

	keys := make([]group, 0, len(groups))
	for g := range groups {
		keys = append(keys, g)
	}
	slices.SortFunc(keys, func(a, b group) int {
		return cmp.Or(cmp.Compare(a.reason, b.reason), cmp.Compare(a.host, b.host))
	})

	events := make([]CopyFailureEvent, 0, len(keys))
	for _, g := range keys {
		refs := groups[g]
		message := fmt.Sprintf("Copy of %s failed with %s", refs[0], g.reason)
		if len(refs) > 1 {
			message = fmt.Sprintf("%d images under %s failed with %s", len(refs), commonPrefix(g.host, refs), g.reason)
		}
		events = append(events, CopyFailureEvent{Reason: g.reason, Message: message})
	}
	return events
}

// commonPrefix is the narrowest path all refs, on host, sit under: their repository when they
// share one, else their longest common directory, the host at least.
func commonPrefix(host string, refs []string) string {
	var common []string
	for i, s := range refs {
		ref, err := imagepath.Parse(s)
		if err != nil {
			return host + "/"
		}
		if i == 0 {
			common = slices.Clone(ref.Segments)
			continue
		}
		n := 0
		for n < len(common) && n < len(ref.Segments) && common[n] == ref.Segments[n] {
			n++
		}
		common = common[:n]
	}
	prefix := strings.Join(append([]string{host}, common...), "/")
	for _, s := range refs {
		if ref, _ := imagepath.Parse(s); len(ref.Segments) != len(common) {
			return prefix + "/"
		}
	}
	return prefix
}
