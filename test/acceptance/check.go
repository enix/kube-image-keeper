package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os/exec"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// maxErrorLines bounds the error lines printed per pod; the count is always printed.
const maxErrorLines = 20

func checkCommand() *command {
	var version string
	return &command{
		usage: "print the state of the installed kuik: the Helm release, the pods, their error lines, the version " +
			"each webhook replica reports, the log levels and the admission policies; reads only",
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&version, "kuik-version", "",
				"version under test, for example 3.0.0-alpha.3: printed next to what the cluster reports")
		},
		run: func(ctx context.Context, e *env, _ []string) error { return e.check(ctx, version) },
	}
}

func (e *env) check(ctx context.Context, version string) error {
	if version != "" {
		// The chart and the build carry 3.0.0-alpha.3, the tag v3.0.0-alpha.3: accept both.
		v, err := parseVersion(version)
		if err != nil {
			return fmt.Errorf("--kuik-version: %w", err)
		}
		version = strings.TrimPrefix(v, "v")
	}
	section("Helm release")
	e.printRelease(ctx, version)
	section("verbosity")
	e.printVerbosity(ctx)
	section("test config")
	e.printTestConfig(ctx)
	section("kuik pods")
	e.printKuikPods(ctx)
	section("kuik_build_info of each webhook replica")
	e.printBuildInfo(ctx, version)
	section("admission policies that could reject the test pods")
	e.printAdmissionPolicies(ctx)
	return nil
}

// helm runs helm read-only on the pinned kubeconfig and context.
func (e *env) helm(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--kubeconfig", e.kubeconfig, "--kube-context", e.kubeContext, "-n", e.kuikNamespace}, args...)
	return exec.CommandContext(ctx, "helm", full...).Output()
}

func (e *env) printRelease(ctx context.Context, version string) {
	out, err := e.helm(ctx, "list", "-o", "json")
	if err != nil {
		fmt.Printf("cannot list the Helm releases of %s: %v\n", e.kuikNamespace, err)
		return
	}
	var releases []struct {
		Name       string `json:"name"`
		Chart      string `json:"chart"`
		AppVersion string `json:"app_version"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(out, &releases); err != nil {
		fmt.Printf("cannot read helm list: %v\n", err)
		return
	}
	for _, r := range releases {
		if r.Name == e.release {
			fmt.Printf("release %s: chart %s, app version %s, status %s\n", r.Name, r.Chart, r.AppVersion, r.Status)
			if version != "" {
				fmt.Printf("expected: chart kube-image-keeper-%s, app version %s, status deployed\n", version, version)
			}
			return
		}
	}
	fmt.Printf("no Helm release %s in %s\n", e.release, e.kuikNamespace)
}

// printVerbosity prints the Helm values verbosity as they are, empty included (an empty
// process value inherits the root one), and the level each process runs at.
func (e *env) printVerbosity(ctx context.Context) {
	out, err := e.helm(ctx, "get", "values", e.release, "--all", "-o", "json")
	if err != nil {
		fmt.Printf("cannot read the Helm values of %s: %v\n", e.release, err)
	} else {
		var values struct {
			Verbosity    string                     `json:"verbosity"`
			Webhook      struct{ Verbosity string } `json:"webhook"`
			Reconciler   struct{ Verbosity string } `json:"reconciler"`
			SecretSyncer struct{ Verbosity string } `json:"secretSyncer"`
		}
		if err := json.Unmarshal(out, &values); err != nil {
			fmt.Printf("cannot read the Helm values: %v\n", err)
		}
		fmt.Printf("Helm values: verbosity %q, webhook.verbosity %q, reconciler.verbosity %q, secretSyncer.verbosity %q\n",
			values.Verbosity, values.Webhook.Verbosity, values.Reconciler.Verbosity, values.SecretSyncer.Verbosity)
	}
	for _, process := range []string{webhookComponent, "reconciler", "secret-syncer"} {
		var deploy appsv1.Deployment
		key := client.ObjectKey{Namespace: e.kuikNamespace, Name: e.fullname() + "-" + process}
		if err := e.client.Get(ctx, key, &deploy); err != nil {
			fmt.Printf("%s: cannot read Deployment %s: %v\n", process, key.Name, err)
			continue
		}
		level := "unknown"
		container := deploy.Spec.Template.Spec.Containers[0]
		for _, arg := range append(container.Command, container.Args...) {
			if v, ok := strings.CutPrefix(arg, "-zap-log-level="); ok {
				level = v
			}
		}
		fmt.Printf("%s logs at %s\n", process, level)
	}
}

func (e *env) printKuikPods(ctx context.Context) {
	pods, err := e.kuikPods(ctx, "")
	if err != nil {
		fmt.Printf("cannot list the kuik pods: %v\n", err)
		return
	}
	if len(pods) == 0 {
		fmt.Printf("no pod of release %s in %s\n", e.release, e.kuikNamespace)
	}
	for _, pod := range pods {
		ready := "False"
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady {
				ready = string(c.Status)
			}
		}
		var restarts int32
		for _, s := range pod.Status.ContainerStatuses {
			restarts += s.RestartCount
		}
		fmt.Printf("%s: phase %s, Ready %s, restarts %d, started %s\n", pod.Name, pod.Status.Phase, ready, restarts,
			pod.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"))
		logs, err := e.clientset.CoreV1().Pods(e.kuikNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
		if err != nil {
			fmt.Printf("  cannot read the logs: %v\n", err)
			continue
		}
		var errorLines []string
		for line := range strings.Lines(string(logs)) {
			if errorLine.MatchString(line) {
				errorLines = append(errorLines, strings.TrimSpace(line))
			}
		}
		fmt.Printf("  %d error line(s) in the logs\n", len(errorLines))
		for _, line := range errorLines[:min(len(errorLines), maxErrorLines)] {
			fmt.Printf("  %s\n", line)
		}
	}
}

// printBuildInfo prints kuik_build_info of every webhook replica, and the commit of tag
// v<version> when the tag is known locally, to compare with its revision.
func (e *env) printBuildInfo(ctx context.Context, version string) {
	if version != "" {
		out, err := exec.CommandContext(ctx, "git", "rev-parse", "-q", "--verify", "v"+version+"^{commit}").Output()
		if err != nil {
			fmt.Printf("tag v%s not found locally, run git fetch --tags to compare the revision\n", version)
		} else {
			fmt.Printf("expected: version %q, revision %q (commit of tag v%s)\n",
				version, strings.TrimSpace(string(out)), version)
		}
	}
	pods, err := e.kuikPods(ctx, webhookComponent)
	if err != nil {
		fmt.Printf("cannot list the webhook pods: %v\n", err)
		return
	}
	for _, pod := range pods {
		lines, err := e.metricLines(ctx, pod.Name)
		if err != nil {
			fmt.Printf("[%s] cannot read the metrics: %v\n", pod.Name, err)
			continue
		}
		info := "no kuik_build_info series"
		for _, line := range lines {
			if strings.HasPrefix(line, "kuik_build_info{") {
				info = line
			}
		}
		fmt.Printf("[%s] %s\n", pod.Name, info)
	}
}

// printAdmissionPolicies lists the Kyverno ClusterPolicies that enforce somewhere, overrides
// included, and the ValidatingAdmissionPolicies: either could reject a test pod.
func (e *env) printAdmissionPolicies(ctx context.Context) {
	policies := &unstructured.UnstructuredList{}
	policies.SetGroupVersionKind(schema.GroupVersionKind{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicyList"})
	switch err := e.client.List(ctx, policies); {
	case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		fmt.Println("no Kyverno kyverno.io/v1 API")
	case err != nil:
		fmt.Printf("cannot list the Kyverno ClusterPolicies, check them by hand: %v\n", err)
	default:
		enforced := 0
		for _, policy := range policies.Items {
			if kyvernoEnforces(policy.Object) {
				enforced++
				fmt.Printf("Kyverno ClusterPolicy %s enforces somewhere\n", policy.GetName())
			}
		}
		fmt.Printf("%d of %d Kyverno ClusterPolicies enforce\n", enforced, len(policies.Items))
	}
	var vaps admissionregistrationv1.ValidatingAdmissionPolicyList
	if err := e.client.List(ctx, &vaps); err != nil {
		fmt.Printf("cannot list the ValidatingAdmissionPolicies, check them by hand: %v\n", err)
		return
	}
	for _, vap := range vaps.Items {
		fmt.Printf("ValidatingAdmissionPolicy %s\n", vap.Name)
	}
	fmt.Printf("%d ValidatingAdmissionPolicies\n", len(vaps.Items))
}

// kyvernoEnforces tells whether a ClusterPolicy enforces in any namespace: its action, a rule's
// action, or an override of either.
func kyvernoEnforces(policy map[string]any) bool {
	spec, _ := policy["spec"].(map[string]any)
	action, _ := spec["validationFailureAction"].(string)
	if strings.EqualFold(action, "enforce") || overridesEnforce(spec["validationFailureActionOverrides"]) {
		return true
	}
	rules, _ := spec["rules"].([]any)
	for _, rule := range rules {
		r, _ := rule.(map[string]any)
		validate, _ := r["validate"].(map[string]any)
		ruleAction, _ := validate["failureAction"].(string)
		if strings.EqualFold(ruleAction, "enforce") || overridesEnforce(validate["failureActionOverrides"]) {
			return true
		}
	}
	return false
}

// overridesEnforce reports whether a Kyverno list of failure action overrides enforces in any
// namespace: the test namespaces may be among them.
func overridesEnforce(overrides any) bool {
	list, _ := overrides.([]any)
	for _, override := range list {
		o, _ := override.(map[string]any)
		if action, _ := o["action"].(string); strings.EqualFold(action, "enforce") {
			return true
		}
	}
	return false
}
