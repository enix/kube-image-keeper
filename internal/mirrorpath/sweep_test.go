package mirrorpath

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Sweep", func() {
	Context("the tags it reasons about", func() {
		PIt("keeps only the tags ending in this cluster's suffix", func() {})
		PIt("never records as unused the truncated and hashed tag of a long reference still desired", func() {})
	})

	Context("pendingDeletion", func() {
		PIt("records a listed tag of this cluster outside the expected set, with no origin", func() {})
		PIt("records the tags of a reference whose last pod went, with its origin", func() {})
		PIt("never records a tag another desired reference still expects, a tagged and a pinned reference sharing it", func() {})
		PIt("leaves the unusedSince of an entry already recorded untouched", func() {})
		PIt("drops an entry whose tag the desired state expects again", func() {})
	})

	Context("retention", func() {
		PIt("deletes an entry whose unusedSince is older than cleanup.retention", func() {})
		PIt("holds an entry whose retention has not elapsed", func() {})
		PIt("holds an entry younger than the deletion buffer under a retention of 0, and deletes it once older", func() {})
	})

	Context("repositories", func() {
		PIt("retires a repository where no tag of this cluster remains, whatever other clusters hold there", func() {})
		PIt("keeps a repository where a tag of this cluster remains", func() {})
	})
})
