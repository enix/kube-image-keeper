package plan

import (
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath"
)

// DeletionBuffer is how long a tag is held under a cleanup.retention shorter than it, 0
// included: a rollout takes a reference out of use and back within seconds, and deleting at
// once would make every rollout delete a tag and copy it again.
// rollout takes a reference out of use and back within seconds, and deleting at once would
// make every rollout delete a tag and copy it again.
const DeletionBuffer = 10 * time.Second

// Release records in pending the destination tags of released, the references whose last
// pod went, with their origin and unusedSince set to now. A tag one of live still expects
// is not recorded, and an entry whose tag live expects leaves pending. An entry already
// recorded keeps its unusedSince.
func Release(clusterID, path string, released, live []imagepath.Reference, pending []kuikv1alpha1.PendingDeletion, now time.Time) []kuikv1alpha1.PendingDeletion {
	expected := expectedTags(clusterID, path, live)
	out := unexpected(pending, expected)
	recorded := refsOf(out)
	for _, ref := range released {
		for _, tag := range destinationTags(clusterID, path, ref) {
			if expected[tag] || recorded[tag] {
				continue
			}
			recorded[tag] = true
			out = append(out, kuikv1alpha1.PendingDeletion{Ref: tag, Origin: ref.String(), UnusedSince: metav1.NewTime(now)})
		}
	}
	return out
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
	expected := expectedTags(s.ClusterID, s.Path, s.Live)
	result := SweepResult{Pending: unexpected(s.Pending, expected)}
	recorded := refsOf(result.Pending)

	suffix := "_" + s.ClusterID
	for _, repository := range slices.Sorted(maps.Keys(s.Listed)) {
		own := slices.DeleteFunc(slices.Clone(s.Listed[repository]), func(tag string) bool { return !strings.HasSuffix(tag, suffix) })
		if len(own) == 0 {
			result.Retire = append(result.Retire, repository)
			continue
		}
		slices.Sort(own)
		for _, tag := range own {
			ref := repository + ":" + tag
			if expected[ref] || recorded[ref] {
				continue
			}
			recorded[ref] = true
			result.Pending = append(result.Pending, kuikv1alpha1.PendingDeletion{Ref: ref, UnusedSince: metav1.NewTime(s.Now)})
		}
	}

	hold := max(s.Retention, DeletionBuffer)
	for _, entry := range result.Pending {
		if s.Now.Sub(entry.UnusedSince.Time) >= hold {
			result.Delete = append(result.Delete, entry.Ref)
		}
	}
	return result
}

// destinationTags are the destination tag references a copy of ref pushes.
func destinationTags(clusterID, path string, ref imagepath.Reference) []string {
	// A reference without tag nor digest renders as its destination repository.
	repository := mirrorpath.Destination(path, imagepath.Reference{Host: ref.Host, Segments: ref.Segments}, clusterID)
	tags := mirrorpath.Tags(ref, clusterID)
	for i, tag := range tags {
		tags[i] = repository + ":" + tag
	}
	return tags
}

// expectedTags are the destination tag references refs expect.
func expectedTags(clusterID, path string, refs []imagepath.Reference) map[string]bool {
	expected := map[string]bool{}
	for _, ref := range refs {
		for _, tag := range destinationTags(clusterID, path, ref) {
			expected[tag] = true
		}
	}
	return expected
}

// unexpected keeps the entries of pending whose tag expected does not hold.
func unexpected(pending []kuikv1alpha1.PendingDeletion, expected map[string]bool) []kuikv1alpha1.PendingDeletion {
	return slices.DeleteFunc(slices.Clone(pending), func(e kuikv1alpha1.PendingDeletion) bool { return expected[e.Ref] })
}

// refsOf indexes the tags of pending.
func refsOf(pending []kuikv1alpha1.PendingDeletion) map[string]bool {
	refs := make(map[string]bool, len(pending))
	for _, e := range pending {
		refs[e.Ref] = true
	}
	return refs
}
