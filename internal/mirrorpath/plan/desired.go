// Package plan computes what an ImageMirror owes its destination from what the cluster runs:
// the references it keeps there, the tags it holds before deleting them, and the counts its
// status reports. Everything runs forward, from the desired state to the destination. See
// docs/v3/walkthroughs/02-imagemirror-reconciliation.md.
package plan

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
)

// Mirror is what the plan reads off an ImageMirror's spec.
type Mirror struct {
	// Path is destination.path.
	Path string
	// ExcludeImages are the excludeImages globs.
	ExcludeImages []string
	// CleanupEnabled is cleanup.enabled.
	CleanupEnabled bool
}

// Desired returns the origin references m keeps at its destination, sorted and unique: the
// origin of every container of pods, the pods m selects, and with cleanup the origin of every
// pendingDeletion entry that carries one. A reference excluded by excludeImages or living
// under m's own destination is left out.
func Desired(m Mirror, pods []*corev1.Pod, pending []kuikv1alpha1.PendingDeletion) []imagepath.Reference {
	desired := map[string]imagepath.Reference{}
	add := func(origin string) {
		ref, err := imagepath.Parse(origin)
		if err != nil || mirrorpath.Excluded(m.ExcludeImages, ref) || mirrorpath.UnderDestination(m.Path, ref) {
			return
		}
		desired[ref.String()] = ref
	}

	for _, pod := range pods {
		// The webhook never routes a static pod, so the mirror could never serve its images.
		if !attribution.Live(pod) || attribution.Static(pod) {
			continue
		}
		for _, c := range attribution.Containers(pod) {
			add(c.Origin)
		}
	}
	// Without cleanup nothing is ever deleted, so holding a reference past its last pod buys
	// nothing: the tag already at the destination stays there anyway.
	if m.CleanupEnabled {
		for _, entry := range pending {
			if entry.Origin != "" {
				add(entry.Origin)
			}
		}
	}

	refs := slices.Collect(maps.Values(desired))
	slices.SortFunc(refs, func(a, b imagepath.Reference) int { return strings.Compare(a.String(), b.String()) })
	return refs
}
