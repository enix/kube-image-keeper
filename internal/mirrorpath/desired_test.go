package mirrorpath

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Desired", func() {
	Context("from the containers of the selected pods", func() {
		PIt("takes each container's origin, never the mirror reference a rewritten container runs", func() {})
		PIt("keeps the same image pulled from two registries as two references", func() {})
		PIt("keeps a tagged reference and the same image pinned by digest as two references", func() {})
	})

	Context("exclusions", func() {
		PIt("leaves out a reference matching excludeImages", func() {})
		PIt("leaves out a reference under its own destination.path, whatever excludeImages says", func() {})
	})

	Context("retained references", func() {
		PIt("keeps a pendingDeletion entry carrying an origin while cleanup is enabled", func() {})
		PIt("leaves out a pendingDeletion entry carrying no origin", func() {})
		PIt("holds no reference past its last pod when cleanup is disabled", func() {})
	})
})
