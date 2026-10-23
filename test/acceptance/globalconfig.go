package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/enix/kube-image-keeper/internal/config"
)

const (
	// configFixtureSuffix ends the name of a config fixture: a piece of kuik's global config that
	// config merges in, never an object setup or apply creates.
	configFixtureSuffix = ".config.yaml"
	// reloadedLine and rejectedLine are what internal/config logs when a process takes a new
	// config, or keeps the previous one.
	reloadedLine = "Reloaded global config"
	rejectedLine = "Rejected global config reload"
)

// errReloadTimeout is returned when the config was written but some kuik pod did not log its
// reload in time.
var errReloadTimeout = errors.New("not every kuik pod reloaded the config in time")

type configFlags struct {
	file        string
	restore     bool
	timeout     time.Duration
	kuikVersion string
}

func configCommand() *command {
	f := &configFlags{}
	return &command{
		usage: "merge a config fixture into the config.yaml of kuik's global config ConfigMap, suspending its Flux " +
			"HelmRelease first, or put back the config of before the first change with --restore; waits for every " +
			"kuik pod to reload",
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&f.file, "file", "", "config fixture of test/acceptance/fixtures/ to merge over the original "+
				"config, a *"+configFixtureSuffix)
			fs.BoolVar(&f.restore, "restore", false, "put back the config of before the first change and resume the HelmRelease")
			fs.DurationVar(&f.timeout, "timeout", 3*time.Minute, "how long to wait for every kuik pod to reload the config")
			fs.StringVar(&f.kuikVersion, "kuik-version", "",
				"version under test, for example 3.0.0-alpha.6: a fixture whose since line is newer is refused")
		},
		run: func(ctx context.Context, e *env, _ []string) error {
			if (f.file == "") == !f.restore {
				return errors.New("give either --file or --restore")
			}
			if err := e.checkKuikNamespace(ctx); err != nil {
				return err
			}
			if f.restore {
				return e.restoreConfig(ctx, f.timeout)
			}
			return e.changeConfig(ctx, f)
		},
	}
}

// readConfigFixture reads a config fixture of test/acceptance/fixtures/, from the copy compiled
// into the program: a file elsewhere is refused.
func readConfigFixture(file string) ([]byte, error) {
	if !strings.HasSuffix(file, configFixtureSuffix) {
		return nil, fmt.Errorf("%s: config takes a *%s file", file, configFixtureSuffix)
	}
	path, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Join("test", "acceptance", "fixtures"))
	if err != nil {
		return nil, err
	}
	if filepath.Dir(path) != dir {
		return nil, fmt.Errorf("%s: config takes a fixture of test/acceptance/fixtures/ only", file)
	}
	raw, err := fixtures.ReadFile(fixtureDir + filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("%s: not a fixture of the program: %w", file, err)
	}
	return raw, nil
}

// changeConfig merges the file over the original config, the one of before the first change of
// the run, validates the result as kuik would, and writes it. The first change records the
// original and suspends the Flux HelmRelease first, so Flux does not put the chart's config back.
func (e *env) changeConfig(ctx context.Context, f *configFlags) error {
	file := f.file
	raw, err := readConfigFixture(file)
	if err != nil {
		return err
	}
	since, err := fixtureSince(file, raw)
	if err != nil {
		return err
	}
	if f.kuikVersion != "" {
		version, err := parseVersion(f.kuikVersion)
		if err != nil {
			return fmt.Errorf("--kuik-version: %w", err)
		}
		if semver.Compare(since, version) > 0 {
			return fmt.Errorf("%s: since %s, newer than --kuik-version %s", file, strings.TrimPrefix(since, "v"), f.kuikVersion)
		}
	}
	piece, err := expand(file, raw, e.vars(&privateFlags{}))
	if err != nil {
		return err
	}
	var cm corev1.ConfigMap
	if err := e.client.Get(ctx, e.configMapKey(), &cm); err != nil {
		return fmt.Errorf("read the global config: %w", err)
	}
	current, hasKey := cm.Data[configKey]
	original, first := current, true
	if stored, ok := cm.Annotations[originalAnnotation]; ok {
		original, first = stored, false
	}
	merged, err := mergeConfig(original, piece)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if _, err := loadConfig([]byte(merged)); err != nil {
		return fmt.Errorf("%s merged over the original config: kuik would reject it, nothing written: %w", file, err)
	}
	if hasKey && merged == current {
		fmt.Printf("%s already holds this config: nothing written\n", describe(&cm))
		return nil
	}
	if first {
		if err := e.recordOriginal(ctx, &cm, current, hasKey); err != nil {
			return err
		}
	} else {
		e.printReleaseState(ctx, &cm)
	}
	written, err := e.writeConfig(ctx, &cm, map[string]*string{configKey: &merged}, nil)
	if err != nil {
		return err
	}
	return e.waitReload(ctx, current, merged, written, f.timeout)
}

// recordOriginal writes, before anything else changes, the original config.yaml and whether the
// program is about to suspend the Flux HelmRelease, then suspends it. A HelmRelease without Flux
// labels, or already suspended by someone else, is left alone: that one stays suspended at
// restore. A suspension that fails leaves the record: --restore then sets spec.suspend to false,
// as it was.
func (e *env) recordOriginal(ctx context.Context, cm *corev1.ConfigMap, original string, hasKey bool) error {
	annotations := map[string]*string{originalAnnotation: &original}
	if !hasKey {
		annotations[originalAbsentAnnotation] = new(labelTrue)
	}
	suspend := false
	if key, ok := fluxRelease(cm); !ok {
		fmt.Printf("%s carries no Flux labels: no HelmRelease to suspend\n", describe(cm))
	} else {
		suspended, err := e.releaseSuspended(ctx, key)
		if err != nil {
			return err
		}
		if suspended {
			fmt.Printf("HelmRelease %s already suspended: it stays so at restore\n", key)
		} else {
			suspend = true
			annotations[suspendedAnnotation] = new(labelTrue)
		}
	}
	if _, err := e.writeConfig(ctx, cm, nil, annotations); err != nil {
		return err
	}
	if suspend {
		return e.suspendRelease(ctx, cm, true)
	}
	return nil
}

// printReleaseState prints whether the Flux HelmRelease of cm is still suspended.
func (e *env) printReleaseState(ctx context.Context, cm *corev1.ConfigMap) {
	key, ok := fluxRelease(cm)
	if !ok {
		return
	}
	suspended, err := e.releaseSuspended(ctx, key)
	switch {
	case err != nil:
		fmt.Println(err)
	case !suspended:
		fmt.Printf("HelmRelease %s is no longer suspended: Flux may put the chart's config back\n", key)
	default:
		fmt.Printf("HelmRelease %s suspended\n", key)
	}
}

// restoreConfig puts the stored original back, then resumes the HelmRelease when the program
// suspended it, and only then removes the record of the suspension: a resume that fails leaves it,
// and a later --restore resumes again. With no record there is nothing to do.
func (e *env) restoreConfig(ctx context.Context, timeout time.Duration) error {
	var cm corev1.ConfigMap
	if err := e.client.Get(ctx, e.configMapKey(), &cm); err != nil {
		return fmt.Errorf("read the global config: %w", err)
	}
	original, hasOriginal := cm.Annotations[originalAnnotation]
	_, hasSuspended := cm.Annotations[suspendedAnnotation]
	if !hasOriginal && !hasSuspended {
		fmt.Printf("%s holds no test config: nothing to restore\n", describe(&cm))
		return nil
	}
	resume := cm.Annotations[suspendedAnnotation] == labelTrue
	current, hasKey := cm.Data[configKey]
	absent := cm.Annotations[originalAbsentAnnotation] == labelTrue
	var written time.Time
	if hasOriginal {
		annotations := map[string]*string{originalAnnotation: nil, originalAbsentAnnotation: nil}
		if !resume {
			annotations[suspendedAnnotation] = nil
		}
		var data map[string]*string
		switch {
		case absent && hasKey:
			data = map[string]*string{configKey: nil}
		case !absent && (!hasKey || current != original):
			data = map[string]*string{configKey: &original}
		}
		var err error
		if written, err = e.writeConfig(ctx, &cm, data, annotations); err != nil {
			return err
		}
		if data == nil {
			hasOriginal = false
			fmt.Printf("%s already held the original config: no reload to wait for\n", configKey)
		}
	}
	if resume {
		if err := e.resume(ctx, &cm); err != nil {
			return err
		}
	} else if key, ok := fluxRelease(&cm); ok {
		fmt.Printf("HelmRelease %s was not suspended by the acceptance test: left as it is\n", key)
	}
	switch {
	case !hasOriginal:
		return nil
	case absent:
		fmt.Printf("%s removed, as it was: kuik keeps the config it runs, and logs a rejected reload\n", configKey)
		return nil
	}
	return e.waitReload(ctx, current, original, written, timeout)
}

// resume sets spec.suspend of the HelmRelease back to false, then removes the record of the
// suspension, read again first: Flux may have written the ConfigMap meanwhile.
func (e *env) resume(ctx context.Context, cm *corev1.ConfigMap) error {
	if _, ok := fluxRelease(cm); ok {
		if err := e.suspendRelease(ctx, cm, false); err != nil {
			return fmt.Errorf("%w: the record of the suspension stays, run --restore again", err)
		}
	} else {
		fmt.Printf("%s no longer carries Flux labels: no HelmRelease to resume\n", describe(cm))
	}
	if err := e.client.Get(ctx, e.configMapKey(), cm); err != nil {
		return fmt.Errorf("read the global config: %w", err)
	}
	_, err := e.writeConfig(ctx, cm, nil, map[string]*string{suspendedAnnotation: nil})
	return err
}

// waitReload waits for every running kuik pod to log reloadedLine after written, and prints that
// line, one per pod. A pod whose containers all started after written loaded the new config at
// startup and logs no reload: it counts as reloaded. A config kuik reads the same as before logs
// nothing: it is not waited for.
func (e *env) waitReload(ctx context.Context, before, after string, written time.Time, timeout time.Duration) error {
	if !expectReload(before, after) {
		fmt.Println("kuik reads this config the same as the previous one: no reload to wait for")
		return nil
	}
	fmt.Printf("waiting up to %s for every running kuik pod to reload the config\n", timeout)
	deadline := time.Now().Add(timeout)
	done := map[string]bool{}
	rejected := 0
	for {
		pods, err := e.kuikPods(ctx, "")
		if err != nil {
			return fmt.Errorf("list the kuik pods: %w", err)
		}
		pending := map[string]string{}
		for _, pod := range pods {
			if pod.Status.Phase != corev1.PodRunning || done[pod.Name] {
				continue
			}
			if startedAfter(&pod, written) {
				fmt.Printf("[%s] started after the write: loaded the new config at startup\n", pod.Name)
				done[pod.Name] = true
				continue
			}
			line, err := e.reloadLine(ctx, pod.Name, written)
			switch {
			case err != nil:
				pending[pod.Name] = err.Error()
			case line == "":
				pending[pod.Name] = "no reload logged yet"
			case strings.Contains(line, rejectedLine):
				fmt.Printf("[%s] REJECTED %s\n", pod.Name, line)
				rejected++
				done[pod.Name] = true
			default:
				fmt.Printf("[%s] %s\n", pod.Name, line)
				done[pod.Name] = true
			}
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			for name, why := range pending {
				fmt.Printf("[%s] did not reload within %s: %s\n", name, timeout, why)
			}
			return errReloadTimeout
		}
		time.Sleep(5 * time.Second)
	}
	if rejected > 0 {
		return fmt.Errorf("%d kuik pods rejected the config", rejected)
	}
	return nil
}

// startedAfter tells whether every container of pod runs and started after written.
func startedAfter(pod *corev1.Pod, written time.Time) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, s := range pod.Status.ContainerStatuses {
		if s.State.Running == nil || !s.State.Running.StartedAt.After(written) {
			return false
		}
	}
	return true
}

// reloadLine returns the first reload line, taken or rejected, the pod logged at or after
// written, without its timestamp; empty when there is none yet.
func (e *env) reloadLine(ctx context.Context, pod string, written time.Time) (string, error) {
	since := metav1.NewTime(written.Add(-time.Second))
	options := &corev1.PodLogOptions{SinceTime: &since, Timestamps: true}
	logs, err := e.clientset.CoreV1().Pods(e.kuikNamespace).GetLogs(pod, options).DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot read the logs: %w", err)
	}
	for line := range strings.Lines(string(logs)) {
		stamp, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || at.Before(written) {
			continue
		}
		if strings.Contains(rest, reloadedLine) || strings.Contains(rest, rejectedLine) {
			return rest, nil
		}
	}
	return "", nil
}

// expectReload tells whether kuik logs a reload going from before to after: it logs none for a
// config it reads the same, nor for a change of metrics only, ignored until restart.
func expectReload(before, after string) bool {
	if before == after {
		return false
	}
	old, err := loadConfig([]byte(before))
	if err != nil {
		return true
	}
	next, err := loadConfig([]byte(after))
	if err != nil {
		return true
	}
	next.Metrics = old.Metrics
	return !reflect.DeepEqual(old, next)
}

// mergeConfig merges the config fixture piece over the config original as a JSON merge patch
// (RFC 7386): a map merges key by key, any other value replaces, null removes the key. The
// rest of the original stays, so a test config keeps what the cluster's real config sets.
func mergeConfig(original string, piece []byte) (string, error) {
	base := map[string]any{}
	if err := yaml.Unmarshal([]byte(original), &base); err != nil {
		return "", fmt.Errorf("read the original config: %w", err)
	}
	var patch map[string]any
	// Strict: a key written twice is an error, not the last one silently winning.
	if err := yaml.UnmarshalStrict(piece, &patch); err != nil {
		return "", fmt.Errorf("read the config fixture: %w", err)
	}
	if base == nil {
		base = map[string]any{}
	}
	out, err := yaml.Marshal(mergeMaps(base, patch))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func mergeMaps(base, patch map[string]any) map[string]any {
	for key, value := range patch {
		if value == nil {
			delete(base, key)
			continue
		}
		next, isMap := value.(map[string]any)
		current, wasMap := base[key].(map[string]any)
		if isMap && wasMap {
			base[key] = mergeMaps(current, next)
			continue
		}
		if isMap {
			value = mergeMaps(map[string]any{}, next)
		}
		base[key] = value
	}
	return base
}

// loadConfig decodes and validates content as kuik does, with the same entry point.
func loadConfig(content []byte) (*config.Config, error) {
	file, err := os.CreateTemp("", "kuik-acceptance-*.yaml")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return config.Load(file.Name())
}

// printTestConfig tells whether a test config is in place and whether the HelmRelease is
// suspended, for check.
func (e *env) printTestConfig(ctx context.Context) {
	var cm corev1.ConfigMap
	if err := e.client.Get(ctx, e.configMapKey(), &cm); err != nil {
		fmt.Printf("cannot read ConfigMap %s: %v\n", e.configMapKey(), err)
		return
	}
	_, changed := cm.Annotations[originalAnnotation]
	_, suspendedByTest := cm.Annotations[suspendedAnnotation]
	switch {
	case changed:
		fmt.Printf("a test config is in place in %s: task acceptance-config -- ... --restore puts the original back\n",
			describe(&cm))
	case suspendedByTest:
		fmt.Printf("%s holds the original config, but the HelmRelease the test suspended was not resumed: "+
			"task acceptance-config -- ... --restore resumes it\n", describe(&cm))
	default:
		fmt.Printf("no test config in %s\n", describe(&cm))
	}
	key, ok := fluxRelease(&cm)
	if !ok {
		fmt.Println("no Flux labels on it: no HelmRelease")
		return
	}
	suspended, err := e.releaseSuspended(ctx, key)
	if err != nil {
		fmt.Println(err)
		return
	}
	by := ""
	if cm.Annotations[suspendedAnnotation] == labelTrue {
		by = ", by the acceptance test"
	}
	fmt.Printf("HelmRelease %s: suspended %t%s\n", key, suspended, by)
}
