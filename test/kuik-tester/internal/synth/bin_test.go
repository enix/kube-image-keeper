package synth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SleepBinary", func() {
	It("refuses an unknown architecture", func() {
		_, err := SleepBinary("riscv64")
		Expect(err).To(MatchError(ContainSubstring("riscv64")))
		_, err = Layer("riscv64")
		Expect(err).To(MatchError(ContainSubstring("riscv64")))
	})
})
