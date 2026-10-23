package main

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

const (
	webhookComponent = "webhook"
	// annotationPrefix is the prefix of the annotations kuik writes on a pod.
	annotationPrefix = "kuik.enix.io/"
)

type inspectFlags struct {
	since   time.Duration
	grep    string
	metrics string
}

func inspectCommand() *command {
	f := &inspectFlags{}
	return &command{
		usage: "print the test objects, their events, the kuik metrics and log lines about them; " +
			"reads only, never a Secret's data",
		flags: func(fs *flag.FlagSet) {
			fs.DurationVar(&f.since, "since", 30*time.Minute, "how far back to read the kuik logs")
			fs.StringVar(&f.grep, "grep", "kuik-test",
				"regular expression a kuik log line must match to be printed (error lines always are)")
			fs.StringVar(&f.metrics, "metrics", "kuik-test", "regular expression a kuik_* metric line must match to be printed")
		},
		run: func(ctx context.Context, e *env, _ []string) error { return e.inspect(ctx, f) },
	}
}

func (e *env) inspect(ctx context.Context, f *inspectFlags) error {
	grep, err := regexp.Compile(f.grep)
	if err != nil {
		return fmt.Errorf("--grep: %w", err)
	}
	metrics, err := regexp.Compile(f.metrics)
	if err != nil {
		return fmt.Errorf("--metrics: %w", err)
	}
	fmt.Printf("inspected at %s\n", time.Now().UTC().Format(time.RFC3339))
	section("kuik CRs of the test")
	e.printCRs(ctx)
	section("pods of the test namespaces")
	e.printPods(ctx)
	section("Secrets of the test (names, types, labels, keys; never the data)")
	e.printSecrets(ctx)
	section(fmt.Sprintf("events of the last %s", f.since))
	e.printEvents(ctx, time.Now().Add(-f.since))
	section("kuik metrics matching " + f.metrics + ", per pod")
	for _, component := range []string{webhookComponent, "reconciler", "secret-syncer"} {
		e.printMetrics(ctx, component, "kuik_", metrics)
	}
	section(fmt.Sprintf("kuik log lines of the last %s matching %s, and every error line", f.since, f.grep))
	e.printLogs(ctx, f.since, grep)
	return nil
}

func section(title string) {
	fmt.Printf("\n===== %s\n", title)
}

func (e *env) printCRs(ctx context.Context) {
	selector := client.MatchingLabels{ownerLabel: labelTrue}
	var alternatives kuikv1alpha1.ImageAlternativeList
	if err := e.client.List(ctx, &alternatives, selector); err != nil {
		fmt.Printf("cannot list the ImageAlternatives: %v\n", err)
	}
	for _, a := range alternatives.Items {
		printObject("ImageAlternative", a.Name, a.Generation, a.Spec, a.Status)
	}
	var mirrors kuikv1alpha1.ImageMirrorList
	if err := e.client.List(ctx, &mirrors, selector); err != nil {
		fmt.Printf("cannot list the ImageMirrors: %v\n", err)
	}
	for _, m := range mirrors.Items {
		printObject("ImageMirror", m.Name, m.Generation, m.Spec, m.Status)
	}
	var monitors kuikv1alpha1.ImageMonitorList
	if err := e.client.List(ctx, &monitors, selector); err != nil {
		fmt.Printf("cannot list the ImageMonitors: %v\n", err)
	}
	for _, m := range monitors.Items {
		printObject("ImageMonitor", m.Name, m.Generation, m.Spec, m.Status)
	}
	if len(alternatives.Items)+len(mirrors.Items)+len(monitors.Items) == 0 {
		fmt.Println("none")
	}
}

func printObject(kind, name string, generation int64, spec, status any) {
	fmt.Printf("\n--- %s/%s (generation %d)\n", kind, name, generation)
	out, err := yaml.Marshal(map[string]any{"spec": spec, "status": status})
	if err != nil {
		fmt.Printf("cannot print it: %v\n", err)
		return
	}
	fmt.Print(string(out))
}

func (e *env) printPods(ctx context.Context) {
	for _, ns := range testNamespaces {
		var pods corev1.PodList
		if err := e.client.List(ctx, &pods, client.InNamespace(ns)); err != nil {
			fmt.Printf("cannot list the pods of %s: %v\n", ns, err)
			continue
		}
		for _, pod := range pods.Items {
			printPod(&pod)
		}
	}
}

func printPod(pod *corev1.Pod) {
	ready := "False"
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			ready = string(c.Status)
		}
	}
	fmt.Printf("\n--- pod %s/%s: created %s, phase %s, Ready %s, labels %v\n", pod.Namespace, pod.Name,
		pod.CreationTimestamp.UTC().Format(time.RFC3339), pod.Status.Phase, ready, pod.Labels)
	for _, c := range pod.Spec.Containers {
		fmt.Printf("container %s: image %s, pull policy %s\n", c.Name, c.Image, c.ImagePullPolicy)
	}
	for _, s := range pod.Status.ContainerStatuses {
		state := "running"
		switch {
		case s.State.Waiting != nil:
			state = fmt.Sprintf("waiting %s: %s", s.State.Waiting.Reason, s.State.Waiting.Message)
		case s.State.Terminated != nil:
			state = "terminated " + s.State.Terminated.Reason
		}
		fmt.Printf("status %s: %s, image %s, imageID %s, restarts %d\n", s.Name, state, s.Image, s.ImageID, s.RestartCount)
	}
	if len(pod.Spec.ImagePullSecrets) > 0 {
		fmt.Printf("imagePullSecrets: %v\n", pod.Spec.ImagePullSecrets)
	}
	keys := make([]string, 0, len(pod.Annotations))
	for k := range pod.Annotations {
		if strings.HasPrefix(k, annotationPrefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Printf("annotation %s: %s\n", k, pod.Annotations[k])
	}
}

func (e *env) printSecrets(ctx context.Context) {
	namespaces := append([]string{e.kuikNamespace}, testNamespaces...)
	for _, ns := range namespaces {
		var secrets corev1.SecretList
		if err := e.client.List(ctx, &secrets, client.InNamespace(ns)); err != nil {
			fmt.Printf("cannot list the Secrets of %s: %v\n", ns, err)
			continue
		}
		for _, s := range secrets.Items {
			// The kuik namespace holds kuik's own Secrets: only the test's are printed.
			if ns == e.kuikNamespace && !testName.MatchString(s.Name) {
				continue
			}
			printSecret(&s)
		}
	}
}

// printSecret prints what a Secret is without its data: name, type, keys, labels, annotation
// keys and owners.
func printSecret(s *corev1.Secret) {
	keys := make([]string, 0, len(s.Data))
	for k := range s.Data {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	owners := make([]string, 0, len(s.OwnerReferences))
	for _, o := range s.OwnerReferences {
		owners = append(owners, o.Kind+"/"+o.Name)
	}
	// Annotation keys only: kubectl.kubernetes.io/last-applied-configuration holds the whole
	// Secret, data included, when it was created with kubectl apply.
	annotations := make([]string, 0, len(s.Annotations))
	for k := range s.Annotations {
		annotations = append(annotations, k)
	}
	slices.Sort(annotations)
	fmt.Printf("secret %s/%s: type %s, created %s, keys %v, labels %v, annotation keys %v, owners %v\n",
		s.Namespace, s.Name, s.Type, s.CreationTimestamp.UTC().Format(time.RFC3339), keys, s.Labels, annotations, owners)
}

// printEvents prints the events of the test namespaces, then those kuik emitted about its
// cluster-scoped test CRs, through the events.k8s.io API; only those seen after since: the
// events of a CR outlive it, and an earlier run used the same names.
func (e *env) printEvents(ctx context.Context, since time.Time) {
	for _, ns := range testNamespaces {
		events, err := e.clientset.EventsV1().Events(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Printf("cannot list the events of %s: %v\n", ns, err)
			continue
		}
		printEventList(events.Items, since)
	}
	for _, kind := range []string{"ImageAlternative", "ImageMirror", "ImageMonitor"} {
		events, err := e.clientset.EventsV1().Events("").List(ctx,
			metav1.ListOptions{FieldSelector: "regarding.kind=" + kind})
		if err != nil {
			fmt.Printf("cannot list the events of the %ss: %v\n", kind, err)
			continue
		}
		printEventList(slices.DeleteFunc(events.Items, func(ev eventsv1.Event) bool {
			return !testName.MatchString(ev.Regarding.Name)
		}), since)
	}
}

func printEventList(events []eventsv1.Event, since time.Time) {
	events = slices.DeleteFunc(events, func(ev eventsv1.Event) bool { return eventTime(ev).Before(since) })
	slices.SortFunc(events, func(a, b eventsv1.Event) int { return eventTime(a).Compare(eventTime(b)) })
	for _, ev := range events {
		count := int32(1)
		if ev.Series != nil {
			count = ev.Series.Count
		} else if ev.DeprecatedCount > 0 {
			count = ev.DeprecatedCount
		}
		target := ev.Regarding.Kind + "/" + ev.Regarding.Name
		if ev.Regarding.Namespace != "" {
			target = ev.Regarding.Kind + "/" + ev.Regarding.Namespace + "/" + ev.Regarding.Name
		}
		fmt.Printf("%s %s %s %s x%d from %s: %s\n", eventTime(ev).UTC().Format(time.RFC3339), ev.Type, target, ev.Reason,
			count, ev.ReportingController+ev.DeprecatedSource.Component, ev.Note)
	}
}

func eventTime(ev eventsv1.Event) time.Time {
	switch {
	case ev.Series != nil:
		return ev.Series.LastObservedTime.Time
	case !ev.DeprecatedLastTimestamp.IsZero():
		return ev.DeprecatedLastTimestamp.Time
	case !ev.EventTime.IsZero():
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}

// kuikPods selects the pods of the release under test only, not those of another kuik release
// in the same namespace; component narrows them to one process when it is not empty.
func (e *env) kuikPods(ctx context.Context, component string) ([]corev1.Pod, error) {
	selector := client.MatchingLabels{
		"app.kubernetes.io/name":     "kube-image-keeper",
		"app.kubernetes.io/instance": e.release,
	}
	if component != "" {
		selector["app.kubernetes.io/component"] = component
	}
	var pods corev1.PodList
	if err := e.client.List(ctx, &pods, client.InNamespace(e.kuikNamespace), selector); err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// printMetrics prints, for each pod of component, its metric lines that start with prefix
// and match pattern. Each replica counts only what it served: the sum is over the replicas.
func (e *env) printMetrics(ctx context.Context, component, prefix string, match *regexp.Regexp) {
	pods, err := e.kuikPods(ctx, component)
	if err != nil {
		fmt.Printf("cannot list the %s pods: %v\n", component, err)
		return
	}
	for _, pod := range pods {
		lines, err := e.metricLines(ctx, pod.Name)
		if err != nil {
			fmt.Printf("[%s] cannot read the metrics: %v\n", pod.Name, err)
			continue
		}
		printed := 0
		for _, line := range lines {
			if strings.HasPrefix(line, prefix) && match.MatchString(line) {
				fmt.Printf("[%s] %s\n", pod.Name, line)
				printed++
			}
		}
		if printed == 0 {
			fmt.Printf("[%s] no %s series matching %s\n", pod.Name, prefix, match)
		}
	}
}

// metricLines reads the metrics of a kuik pod through the API server's pod proxy.
func (e *env) metricLines(ctx context.Context, pod string) ([]string, error) {
	body, err := e.clientset.CoreV1().Pods(e.kuikNamespace).ProxyGet("", pod, "8080", "metrics", nil).DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var lines []string
	for line := range strings.Lines(string(body)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// errorLine matches a log line an operator must act on, in the JSON and the console encodings.
var errorLine = regexp.MustCompile(`(?i)"level":"(error|dpanic|panic|fatal)"|\sERROR\s`)

func (e *env) printLogs(ctx context.Context, since time.Duration, grep *regexp.Regexp) {
	pods, err := e.kuikPods(ctx, "")
	if err != nil {
		fmt.Printf("cannot list the kuik pods: %v\n", err)
		return
	}
	seconds := int64(since.Seconds())
	for _, pod := range pods {
		options := &corev1.PodLogOptions{SinceSeconds: &seconds}
		logs, err := e.clientset.CoreV1().Pods(e.kuikNamespace).GetLogs(pod.Name, options).DoRaw(ctx)
		if err != nil {
			fmt.Printf("[%s] cannot read the logs: %v\n", pod.Name, err)
			continue
		}
		for line := range strings.Lines(string(logs)) {
			if grep.MatchString(line) || errorLine.MatchString(line) {
				fmt.Printf("[%s] %s", pod.Name, line)
			}
		}
	}
}
