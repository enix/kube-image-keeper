// Package plan computes what an ImageMirror owes its destination from what the cluster runs:
// the references it keeps there, the tags it holds before deleting them, and the counts its
// status reports. Everything runs forward, from the desired state to the destination. See
// docs/v3/walkthroughs/02-imagemirror-reconciliation.md.
package plan

import (
	corev1 "k8s.io/api/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
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
	return nil
}
