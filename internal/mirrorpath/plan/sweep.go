package plan

import (
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// DeletionBuffer is how long a tag is held under a cleanup.retention shorter than it: a
// rollout takes a reference out of use and back within seconds, and deleting at once would
// make every rollout delete a tag and copy it again.
const DeletionBuffer = 10 * time.Second

// Release records in pending the destination tags of released, the references whose last
// pod went, with their origin and unusedSince set to now. A tag one of live still expects
// is not recorded, and an entry whose tag live expects leaves pending. An entry already
// recorded keeps its unusedSince.
func Release(clusterID, path string, released, live []imagepath.Reference, pending []kuikv1alpha1.PendingDeletion, now time.Time) []kuikv1alpha1.PendingDeletion {
	return pending
}

// Sweep is the tag sweep of one destination pass. See walkthrough 02, "C.3 Sweep the
// repositories".
type Sweep struct {
	// ClusterID is the suffix of the tags this cluster owns.
	ClusterID string
	// Path is destination.path.
	Path string
	// Retention is cleanup.retention.
	Retention time.Duration
	// Live are the references the selected pods run now.
	Live []imagepath.Reference
	// Listed maps each repository of status.repositories this pass listed to all its tags,
	// every page read. A repository whose listing failed is absent.
	Listed map[string][]string
	// Pending is status.pendingDeletion.
	Pending []kuikv1alpha1.PendingDeletion
	// Now is when the pass runs.
	Now time.Time
}

// SweepResult is what a sweep decided.
type SweepResult struct {
	// Pending is the new status.pendingDeletion.
	Pending []kuikv1alpha1.PendingDeletion
	// Delete are the destination tag references whose retention elapsed.
	Delete []string
	// Retire are the repositories where no tag of this cluster remains.
	Retire []string
}

// Run diffs the listed tags of this cluster forward against the tags the live references
// expect.
func (s Sweep) Run() SweepResult {
	return SweepResult{}
}
