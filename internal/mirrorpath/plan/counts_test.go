package plan

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Counts", func() {
	PIt("counts as running a reference whose destination reference a container carries", func() {})
	PIt("counts as standby a reference a pod declares whose destination reference no container carries, copied or not", func() {})
	PIt("counts as retained each distinct origin of pendingDeletion, a pinned reference's two tags counting once", func() {})
	PIt("counts as orphan tags the pendingDeletion entries carrying no origin, one per tag", func() {})
	PIt("counts as available the references the last self-check found at the destination, and the rest as unavailable", func() {})
	PIt("counts as available a reference copied since the last self-check", func() {})
	PIt("counts as drifted the driftedImages entries", func() {})
	PIt("counts as missingSource the failedImageCopies entries with reason SourceNotFound", func() {})
})

var _ = Describe("CopyFailureEvents", func() {
	PIt("names a single failing image in its own event", func() {})
	PIt("coalesces the images failing with one reason in a pass under their narrowest common prefix, with their count", func() {})
	PIt("falls back to the host as the prefix of images sharing nothing else", func() {})
	PIt("emits one event per reason when the failures of a pass differ in reason", func() {})
})
