package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// EnvTestBinaryDir locates the envtest binaries downloaded by `task setup-envtest`, so the
// suites also run from an IDE without KUBEBUILDER_ASSETS. The Taskfile keeps them in the
// main checkout's bin/k8s, found from any worktree through the git common dir, or in this
// checkout's bin/k8s outside git. That directory collects the versions of every branch, so
// the last one in name order wins. Returns "" when none is found.
func EnvTestBinaryDir() string {
	base := filepath.Join(checkoutDir(), "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, entry := range slices.Backward(entries) {
		if entry.IsDir() {
			return filepath.Join(base, entry.Name())
		}
	}
	return ""
}

// checkoutDir returns the main checkout, or the root of this source tree outside git.
func checkoutDir() string {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err == nil {
		return filepath.Dir(strings.TrimSpace(string(out)))
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
