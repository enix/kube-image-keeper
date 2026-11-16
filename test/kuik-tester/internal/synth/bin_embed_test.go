//go:build sleepbins

package synth

import (
	"bytes"
	"debug/elf"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SleepBinary (embedded)", func() {
	DescribeTable("embedded binary",
		func(arch string, machine elf.Machine) {
			bin, err := SleepBinary(arch)
			Expect(err).NotTo(HaveOccurred())
			f, err := elf.NewFile(bytes.NewReader(bin))
			Expect(err).NotTo(HaveOccurred())
			Expect(f.Machine).To(Equal(machine))
		},
		Entry("amd64 is an x86-64 ELF", "amd64", elf.EM_X86_64),
		Entry("arm64 is an AArch64 ELF", "arm64", elf.EM_AARCH64),
	)
})
