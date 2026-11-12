package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

// writeConfig writes a minimal configuration with the given state path into
// dir and returns the file's path.
func writeConfig(dir, statePath string) string {
	GinkgoHelper()
	p := filepath.Join(dir, "config.yaml")
	body := "baseDomain: kt.test\nregistryAddr: 127.0.0.1:0\nadminAddr: 127.0.0.1:0\nstatePath: " + statePath + "\n"
	Expect(os.WriteFile(p, []byte(body), 0o600)).To(Succeed())
	return p
}

// readOnlyDir returns a directory under dir that the tests cannot write to.
func readOnlyDir(dir string) string {
	GinkgoHelper()
	ro := filepath.Join(dir, "ro")
	Expect(os.Mkdir(ro, 0o700)).To(Succeed())
	Expect(os.Chmod(ro, 0o500)).To(Succeed())
	DeferCleanup(os.Chmod, ro, os.FileMode(0o700))
	return ro
}

var _ = Describe("checkWritable", func() {
	DescribeTable("checks that the state directory can be written",
		func(setup func(dir string) string, want types.GomegaMatcher) {
			dir := GinkgoT().TempDir()
			statePath := setup(dir)
			Expect(checkWritable(statePath)).To(want)
		},
		Entry("an empty path keeps the state in memory and needs no directory",
			func(string) string { return "" }, Succeed()),
		Entry("a directory that can be written is accepted",
			func(dir string) string { return filepath.Join(dir, "state.json") }, Succeed()),
		Entry("a missing directory is refused, naming it",
			func(dir string) string { return filepath.Join(dir, "missing", "state.json") },
			MatchError(ContainSubstring("state directory"))),
		Entry("a read-only directory is refused",
			func(dir string) string {
				if os.Geteuid() == 0 {
					Skip("root writes into a read-only directory")
				}
				return filepath.Join(readOnlyDir(dir), "state.json")
			},
			MatchError(ContainSubstring("is not writable"))),
	)

	It("leaves no probe file behind in a writable directory", func() {
		dir := GinkgoT().TempDir()
		Expect(checkWritable(filepath.Join(dir, "state.json"))).To(Succeed())
		Expect(os.ReadDir(dir)).To(BeEmpty())
	})
})

var _ = Describe("run", func() {
	It("stops before listening when the state directory is not writable", func() {
		dir := GinkgoT().TempDir()
		// The state directory is a regular file: no user, root included, can write below it.
		notADir := filepath.Join(dir, "file")
		Expect(os.WriteFile(notADir, nil, 0o600)).To(Succeed())
		cfgPath := writeConfig(dir, filepath.Join(notADir, "state.json"))
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- run(cfgPath)
		}()
		Eventually(done).WithTimeout(5 * time.Second).Should(Receive(MatchError(ContainSubstring("not writable"))))
	})

	It("returns the configuration error", func() {
		err := run(filepath.Join(GinkgoT().TempDir(), "missing.yaml"))
		Expect(err).To(MatchError(fs.ErrNotExist))
	})
})
