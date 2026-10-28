package state

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("State file", func() {
	PIt("is created on the first run with the v2 resources and the rewritten pods", func() {})
	PIt("keeps the pods of earlier runs and adds the ones rewritten since", func() {})
	PIt("takes the latest status of the v2 resources while v2 runs", func() {})
	PIt("is left unchanged once v2 is gone", func() {})
	PIt("refuses a file written by a newer format", func() {})
})
