package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	diff := flag.String("diff", "", "print only the cases added and removed since this git ref, instead of the whole tree")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: testoutline [-diff <ref>] [path...]")
		fmt.Fprintln(os.Stderr, "Prints the Ginkgo specs under the given paths (default: the repository)",
			"as a plain-English tree.")
		flag.PrintDefaults()
	}
	flag.Parse()
	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	var err error
	if *diff == "" {
		err = printTree(paths)
	} else {
		err = printDiff(*diff, paths)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testoutline:", err)
		os.Exit(1)
	}
}

// printTree writes the outline of every test file of the working tree under paths.
func printTree(paths []string) error {
	files, err := workingTreeFiles(paths)
	if err != nil {
		return err
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		nodes, err := parse(f, src)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			continue
		}
		fmt.Printf("# %s\n", f)
		writeTree(os.Stdout, nodes, 0)
		fmt.Println()
	}
	return nil
}

// printDiff writes, per test file, the cases the working tree adds to or removes from ref.
func printDiff(ref string, paths []string) error {
	newFiles, err := workingTreeFiles(paths)
	if err != nil {
		return err
	}
	oldFiles, err := refFiles(ref, paths)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var all []string
	for _, f := range append(oldFiles, newFiles...) {
		if !seen[f] {
			seen[f] = true
			all = append(all, f)
		}
	}
	changed := false
	for _, f := range all {
		var oldCases, newCases []string
		if src, err := git("show", ref+":"+f); err == nil {
			nodes, err := parse(f, src)
			if err != nil {
				return err
			}
			oldCases = cases(nodes)
		}
		if src, err := os.ReadFile(f); err == nil {
			nodes, err := parse(f, src)
			if err != nil {
				return err
			}
			newCases = cases(nodes)
		}
		added, removed := diffCases(oldCases, newCases)
		if len(added) == 0 && len(removed) == 0 {
			continue
		}
		changed = true
		fmt.Printf("# %s\n", f)
		for _, c := range removed {
			fmt.Printf("- %s\n", c)
		}
		for _, c := range added {
			fmt.Printf("+ %s\n", c)
		}
		fmt.Println()
	}
	if !changed {
		fmt.Printf("No test case changed since %s.\n", ref)
	}
	return nil
}

// workingTreeFiles lists the tracked and untracked test files under paths.
func workingTreeFiles(paths []string) ([]string, error) {
	out, err := git(append([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	return testFiles(out), nil
}

// refFiles lists the test files under paths at ref.
func refFiles(ref string, paths []string) ([]string, error) {
	out, err := git(append([]string{"ls-tree", "-r", "-z", "--name-only", ref, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	return testFiles(out), nil
}

// testFiles keeps the *_test.go entries of a NUL-separated listing.
func testFiles(listing []byte) []string {
	var files []string
	for f := range strings.SplitSeq(string(listing), "\x00") {
		if strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	return files
}

// git runs a git command in the current directory and returns its output.
func git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
