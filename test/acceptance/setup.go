package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fixtures are the base test objects, one file per test, applied in the order of their names.
//
//go:embed fixtures/*.yaml
var fixtures embed.FS

const (
	// mirrorPrefixFixtures marks the fixtures of test 14, applied only with --mirror-prefix.
	mirrorPrefixFixtures = "14-"
	// privateAlternative is the fixture after which setup waits for the pull Secret of test 11.
	privateAlternative = "11-private-alternative.yaml"
	privateInjected    = "kuik-inject-imagealternative-kuik-test-private"
	// injectedWait bounds that wait.
	injectedWait = 60 * time.Second
)

// sincePrefix starts the first line of every fixture: the kuik version that introduced its test.
const sincePrefix = "# since: "

// privatePrefixes mark the fixtures of tests 11 and 13, applied only with --private-repo and
// --pull-auth.
var privatePrefixes = []string{"11-", "13-"}

// handPrefixes mark the fixtures setup never applies, applied by hand with apply when their
// scenario says: test 16 recreates a pod once the monitors have reported, and the API server
// must refuse the one of test 17.
var handPrefixes = []string{"16-", "17-"}

// privateFlags fill ${PRIVATE_REPO} and ${PULL_AUTH}, for setup and apply alike.
type privateFlags struct {
	privateRepo, pullAuth string
}

func (f *privateFlags) register(flags *flag.FlagSet) {
	flags.StringVar(&f.privateRepo, "private-repo", "",
		"private repository holding nginx-unprivileged:1.31.6-alpine, for the real pull of test 11 "+
			"and the anonymous check of test 13")
	flags.StringVar(&f.pullAuth, "pull-auth", "",
		"docker config Secret in the kuik namespace that can pull --private-repo; the user's, never touched")
}

// vars are the values of the ${NAME} a fixture may use.
func (e *env) vars(f *privateFlags) map[string]string {
	return map[string]string{
		"KUIK_NAMESPACE": e.kuikNamespace, "PRIVATE_REPO": f.privateRepo, "PULL_AUTH": f.pullAuth,
		"MIRROR_PREFIX": e.mirrorPrefix, "PUBLIC_REPO": e.publicRepo,
	}
}

type setupFlags struct {
	privateFlags
	kuikVersion string
}

func setupCommand() *command {
	f := &setupFlags{}
	return &command{
		usage: "apply the fixtures, the base objects of the tests, refusing to start over the objects of an earlier run",
		flags: func(fs *flag.FlagSet) {
			f.register(fs)
			fs.StringVar(&f.kuikVersion, "kuik-version", "",
				"version under test, for example 3.0.0-alpha.3: a fixture whose since line is newer is skipped")
		},
		run: func(ctx context.Context, e *env, _ []string) error { return e.setup(ctx, f) },
	}
}

// setup applies the fixtures through the guard of apply, and prints the time it started (for
// the logs) and the rewrite counters before any test pod existed. It waits for nothing kuik
// does, except the pull Secret of test 11 before its pod, and reports how long that took.
func (e *env) setup(ctx context.Context, f *setupFlags) error {
	if (f.privateRepo == "") != (f.pullAuth == "") {
		return fmt.Errorf("--private-repo and --pull-auth go together")
	}
	var version string
	if f.kuikVersion != "" {
		var err error
		if version, err = parseVersion(f.kuikVersion); err != nil {
			return fmt.Errorf("--kuik-version: %w", err)
		}
	}
	files, err := loadFixtures()
	if err != nil {
		return err
	}
	if err := e.checkKuikNamespace(ctx); err != nil {
		return err
	}
	if err := e.checkPullAuth(ctx, f.pullAuth); err != nil {
		return err
	}
	e.pullAuth = f.pullAuth
	if err := e.refuseLeftovers(ctx); err != nil {
		return err
	}
	vars := e.vars(&f.privateFlags)

	fmt.Printf("setup started at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Println("rewrite counters of the test CRs before setup (each webhook replica counts the admissions it served):")
	e.printMetrics(ctx, webhookComponent, "kuik_routing_rewrites_total", regexp.MustCompile("kuik-test"))
	for _, file := range files {
		base, raw := file.name, file.raw
		if version != "" && semver.Compare(file.since, version) > 0 {
			fmt.Printf("\n== %s: skipped, since %s, newer than --kuik-version %s\n",
				base, strings.TrimPrefix(file.since, "v"), f.kuikVersion)
			continue
		}
		if slices.ContainsFunc(privatePrefixes, func(p string) bool { return strings.HasPrefix(base, p) }) &&
			f.privateRepo == "" {
			fmt.Printf("\n== %s: skipped, no --private-repo and --pull-auth\n", base)
			continue
		}
		if slices.ContainsFunc(handPrefixes, func(p string) bool { return strings.HasPrefix(base, p) }) {
			fmt.Printf("\n== %s: skipped, applied by hand when its scenario says\n", base)
			continue
		}
		if strings.HasPrefix(base, mirrorPrefixFixtures) && e.mirrorPrefix == "" {
			fmt.Printf("\n== %s: skipped, no --mirror-prefix\n", base)
			continue
		}
		expanded, err := expand(base, raw, vars)
		if err != nil {
			return err
		}
		fmt.Printf("\n== %s\n", base)
		if err := e.applyAll(ctx, base, expanded, true); err != nil {
			return err
		}
		if base == privateAlternative {
			if err := e.waitInjected(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// fixtureDir holds the fixtures in the embedded file system.
const fixtureDir = "fixtures/"

// fixtureNames are the base names of the fixtures, in the order setup applies them.
func fixtureNames() ([]string, error) {
	names, err := fs.Glob(fixtures, fixtureDir+"*.yaml")
	if err != nil {
		return nil, err
	}
	for i, name := range names {
		names[i] = strings.TrimPrefix(name, fixtureDir)
	}
	slices.Sort(names)
	return names, nil
}

type fixture struct {
	name  string
	raw   []byte
	since string
}

// loadFixtures reads every fixture and its since line, in the order setup applies them: a
// fixture without one fails the run before anything is written.
func loadFixtures() ([]fixture, error) {
	names, err := fixtureNames()
	if err != nil {
		return nil, err
	}
	files := make([]fixture, len(names))
	for i, name := range names {
		raw, err := fixtures.ReadFile(fixtureDir + name)
		if err != nil {
			return nil, err
		}
		since, err := fixtureSince(name, raw)
		if err != nil {
			return nil, err
		}
		files[i] = fixture{name: name, raw: raw, since: since}
	}
	return files, nil
}

// fixtureSince reads the first line of a fixture, "# since: <version>", the kuik version that
// introduced its test, and returns that version in the canonical form of semver ("v" first).
func fixtureSince(file string, raw []byte) (string, error) {
	first, _, _ := strings.Cut(string(raw), "\n")
	version, ok := strings.CutPrefix(strings.TrimSpace(first), sincePrefix)
	if !ok {
		return "", fmt.Errorf("%s: the first line must be %q followed by the kuik version that introduced the test",
			file, sincePrefix)
	}
	since, err := parseVersion(strings.TrimSpace(version))
	if err != nil {
		return "", fmt.Errorf("%s: since: %w", file, err)
	}
	return since, nil
}

// parseVersion returns a kuik version (3.0.0-alpha.3, or its tag v3.0.0-alpha.3) in the form
// golang.org/x/mod/semver compares, "v" first. It takes the full MAJOR.MINOR.PATCH only: semver
// reads v3.1 as v3.1.0, a since line or a release never writes it so.
func parseVersion(version string) (string, error) {
	v := version
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) || semver.Canonical(v) != v {
		return "", fmt.Errorf("%q is not a version MAJOR.MINOR.PATCH[-PRERELEASE]", version)
	}
	return v, nil
}

// expand replaces the ${NAME} of a fixture with vars; an unknown or empty one is an error, so
// a fixture never reaches the cluster half filled.
func expand(file string, raw []byte, vars map[string]string) ([]byte, error) {
	var missing []string
	out := os.Expand(string(raw), func(name string) string {
		value := vars[name]
		if value == "" {
			missing = append(missing, name)
		}
		return value
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s: no value for %s", file, strings.Join(missing, ", "))
	}
	return []byte(out), nil
}

// waitInjected waits for the pull Secret of test 11, so its pod does not start its real pull
// in a backoff, and prints how long kuik took to provision it.
func (e *env) waitInjected(ctx context.Context) error {
	start := time.Now()
	for {
		err := e.client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: privateInjected}, &corev1.Secret{})
		switch {
		case err == nil:
			fmt.Printf("pull Secret %s/%s appeared after %s\n",
				testNamespace, privateInjected, time.Since(start).Round(time.Second))
			return nil
		case !apierrors.IsNotFound(err):
			return fmt.Errorf("look up %s: %w", privateInjected, err)
		case time.Since(start) > injectedWait:
			fmt.Printf("pull Secret %s/%s not there after %s: applying the pod anyway\n",
				testNamespace, privateInjected, injectedWait)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

// checkPullAuth refuses a --pull-auth the acceptance test could delete: a Secret it labelled
// itself, cleanup would take it for a test object.
func (e *env) checkPullAuth(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	var secret corev1.Secret
	if err := e.client.Get(ctx, client.ObjectKey{Namespace: e.kuikNamespace, Name: name}, &secret); err != nil {
		return fmt.Errorf("--pull-auth %s in %s: %w", name, e.kuikNamespace, err)
	}
	if secret.Labels[ownerLabel] == labelTrue {
		return fmt.Errorf("--pull-auth %s carries %s: cleanup would delete it", name, ownerLabel)
	}
	return nil
}
