package migration

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("migrate", func() {
	PIt("prints the v2 installation, resources, rewritten pods and pull secrets it found", func() {})
	PIt("saves the state file in the current directory", func() {})
	PIt("stops with an explanation when v2 is gone, v2 leftovers remain and no state file exists", func() {})
	PIt("exits 0 when the cluster holds no trace of v2 and no state file exists", func() {})
	PIt("exits non-zero while the migration is not complete", func() {})
})
