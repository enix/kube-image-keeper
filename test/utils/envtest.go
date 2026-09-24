package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// EnvTestBinaryDir locates the envtest binaries downloaded by `task setup-envtest`, so the
// suites also run from an IDE without KUBEBUILDER_ASSETS. The Taskfile keeps them in the
// main checkout's bin/k8s, found from any worktree through the git common dir, or in this
// checkout's bin/k8s outside git. That directory collects the versions of every branch, so
// it picks the Kubernetes minor of this branch's k8s.io/api, as the Taskfile does, and its
// latest patch. Returns "" when none is found.
func EnvTestBinaryDir() string {
	minor := k8sMinor()
	if minor == "" {
		return ""
	}
	base := filepath.Join(checkoutDir(), "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	prefix, suffix := "1."+minor+".", "-"+runtime.GOOS+"-"+runtime.GOARCH
	dir, latest := "", -1
	for _, entry := range entries {
		patch, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok || !entry.IsDir() {
			continue
		}
		patch, ok = strings.CutSuffix(patch, suffix)
		if n, err := strconv.Atoi(patch); ok && err == nil && n > latest {
			dir, latest = filepath.Join(base, entry.Name()), n
		}
	}
	return dir
}

// k8sMinor returns the minor of the k8s.io/api version in go.mod, read as the Taskfile
// reads it: v0.37.1 gives "37". A test binary carries no module dependencies to read it
// from.
func k8sMinor() string {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "k8s.io/api").Output()
	if err != nil {
		return ""
	}
	if parts := strings.Split(strings.TrimSpace(string(out)), "."); len(parts) >= 2 {
		return parts[1]
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
