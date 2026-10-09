//go:build smoke

package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	kuikPods    = "app.kubernetes.io/name=kube-image-keeper"
	webhookPods = kuikPods + ",app.kubernetes.io/component=webhook"
)

// errorLine matches a log line an operator must act on, in the JSON and the console encodings.
var errorLine = regexp.MustCompile(`(?i)"level":"(error|dpanic|panic|fatal)"|\sERROR\s`)

// TestInstall only reads: the release, the kuik pods, their logs and the version they report.
// It fails on what makes the release unfit to test; what only informs the test plan (the
// admission policies, the log levels) is logged with a WARN prefix.
func TestInstall(t *testing.T) {
	release := features.New("the Helm release").
		Assess("is deployed at the expected version", expectRelease).
		Assess("logs its verbosity values and the level of each process", logVerbosity).
		Feature()
	pods := features.New("the kuik pods").
		Assess("are all Running and never restarted", expectPodsRunning).
		Assess("logged no error", expectNoErrorLogs).
		Assess("report the expected version and revision on every webhook replica", expectBuildInfo).
		Feature()
	policies := features.New("the admission policies").
		Assess("are listed when they could reject the test pods", logAdmissionPolicies).
		Feature()
	testenv.Test(t, release, pods, policies)
}

func warnf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("WARN "+format, args...)
}

// helm runs helm read-only on the pinned kubeconfig and context.
func helm(ctx context.Context, cfg *envconf.Config, args ...string) ([]byte, error) {
	full := make([]string, 0, 6+len(args))
	full = append(full, "--kubeconfig", cfg.KubeconfigFile(), "--kube-context", cfg.KubeContext(), "-n", *kuikNamespace)
	return exec.CommandContext(ctx, "helm", append(full, args...)...).Output()
}

func expectRelease(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	out, err := helm(ctx, cfg, "list", "-o", "json")
	if err != nil {
		t.Fatalf("cannot list the Helm releases of %s: %v", *kuikNamespace, err)
	}
	var releases []struct {
		Name       string `json:"name"`
		Chart      string `json:"chart"`
		AppVersion string `json:"app_version"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(out, &releases); err != nil {
		t.Fatal(err)
	}
	for _, r := range releases {
		if r.Name != *release {
			continue
		}
		if r.Status != "deployed" {
			t.Fatalf("release %s is %s", r.Name, r.Status)
		}
		if *kuikVersion != "" && (r.Chart != "kube-image-keeper-"+*kuikVersion || r.AppVersion != *kuikVersion) {
			t.Fatalf("release %s: chart %s, app %s, expected %s", r.Name, r.Chart, r.AppVersion, *kuikVersion)
		}
		t.Logf("release %s: chart %s, app %s, %s", r.Name, r.Chart, r.AppVersion, r.Status)
		return ctx
	}
	t.Fatalf("no Helm release %s in %s", *release, *kuikNamespace)
	return ctx
}

// logVerbosity logs the Helm values verbosity as they are, empty included (an empty process
// value inherits the root one), and the level each process runs at: the skill restores the
// former after the test and asks for debug when the latter is lower.
func logVerbosity(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	out, err := helm(ctx, cfg, "get", "values", *release, "--all", "-o", "json")
	if err != nil {
		t.Fatalf("cannot read the Helm values of %s: %v", *release, err)
	}
	var values struct {
		Verbosity    string                     `json:"verbosity"`
		Webhook      struct{ Verbosity string } `json:"webhook"`
		Reconciler   struct{ Verbosity string } `json:"reconciler"`
		SecretSyncer struct{ Verbosity string } `json:"secretSyncer"`
	}
	if err := json.Unmarshal(out, &values); err != nil {
		t.Fatal(err)
	}
	t.Logf("Helm verbosity: root %q, webhook %q, reconciler %q, secretSyncer %q",
		values.Verbosity, values.Webhook.Verbosity, values.Reconciler.Verbosity, values.SecretSyncer.Verbosity)

	fullname := *release
	if !strings.Contains(fullname, "kube-image-keeper") {
		fullname += "-kube-image-keeper"
	}
	for _, process := range []string{"webhook", "reconciler", "secret-syncer"} {
		deploy, err := clientset.AppsV1().Deployments(*kuikNamespace).Get(ctx, fullname+"-"+process, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("cannot read the Deployment of the %s: %v", process, err)
		}
		level := "unknown"
		container := deploy.Spec.Template.Spec.Containers[0]
		for _, arg := range append(container.Command, container.Args...) {
			if v, ok := strings.CutPrefix(arg, "-zap-log-level="); ok {
				level = v
			}
		}
		if level == "debug" {
			t.Logf("%s logs at debug", process)
		} else {
			warnf(t, "%s logs at %s, not debug", process, level)
		}
	}
	return ctx
}

func expectPodsRunning(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: kuikPods})
	if err != nil {
		t.Fatalf("cannot list the kuik pods: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("no kuik pod in %s", *kuikNamespace)
	}
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodRunning {
			t.Errorf("%s is %s", pod.Name, pod.Status.Phase)
		}
		for _, c := range pod.Status.ContainerStatuses {
			if c.RestartCount > 0 {
				t.Errorf("%s/%s restarted %d times", pod.Name, c.Name, c.RestartCount)
			}
		}
	}
	t.Logf("%d kuik pods Running, none restarted", len(list.Items))
	return ctx
}

func expectNoErrorLogs(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: kuikPods})
	if err != nil {
		t.Fatalf("cannot list the kuik pods: %v", err)
	}
	for _, pod := range list.Items {
		logs, err := clientset.CoreV1().Pods(*kuikNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
		if err != nil {
			t.Fatalf("cannot read the logs of %s: %v", pod.Name, err)
		}
		for line := range strings.Lines(string(logs)) {
			if errorLine.MatchString(line) {
				t.Errorf("%s: %s", pod.Name, strings.TrimSpace(line))
			}
		}
	}
	return ctx
}

// expectBuildInfo checks kuik_build_info on every webhook replica: the version, and the
// revision against the commit of tag v<version> when the tag is known locally.
func expectBuildInfo(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: webhookPods})
	if err != nil || len(list.Items) == 0 {
		t.Fatalf("cannot find the webhook pods: %v", err)
	}
	revision := ""
	if *kuikVersion != "" {
		out, err := exec.CommandContext(ctx, "git", "rev-parse", "-q", "--verify", "v"+*kuikVersion+"^{commit}").Output()
		if err != nil {
			warnf(t, "tag v%s not found locally, revision not compared (git fetch --tags)", *kuikVersion)
		}
		revision = strings.TrimSpace(string(out))
	}
	for _, pod := range list.Items {
		body, err := clientset.CoreV1().Pods(*kuikNamespace).ProxyGet("", pod.Name, "8080", "metrics", nil).DoRaw(ctx)
		if err != nil {
			t.Fatalf("cannot read the metrics of %s: %v", pod.Name, err)
		}
		info := ""
		for line := range strings.Lines(string(body)) {
			if strings.HasPrefix(line, "kuik_build_info{") {
				info = strings.TrimSpace(line)
			}
		}
		switch {
		case info == "":
			t.Errorf("%s exports no kuik_build_info", pod.Name)
		case *kuikVersion != "" && !strings.Contains(info, fmt.Sprintf("version=%q", *kuikVersion)):
			t.Errorf("%s: %s, expected version %s", pod.Name, info, *kuikVersion)
		case revision != "" && !strings.Contains(info, fmt.Sprintf("revision=%q", revision)):
			t.Errorf("%s: %s, expected revision %s", pod.Name, info, revision)
		default:
			t.Logf("%s: %s", pod.Name, info)
		}
	}
	return ctx
}

// logAdmissionPolicies lists the policies that could reject the test pods: Kyverno
// ClusterPolicies in Enforce and the ValidatingAdmissionPolicies. A failed lookup is reported,
// never read as "no policy"; a cluster without Kyverno has none of its policies.
func logAdmissionPolicies(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	switch _, err := clientset.Discovery().ServerResourcesForGroupVersion("kyverno.io/v1"); {
	case apierrors.IsNotFound(err):
		t.Log("no Kyverno kyverno.io/v1 API")
	case err != nil:
		warnf(t, "cannot look up the Kyverno API, check its policies by hand: %v", err)
	default:
		logKyvernoEnforced(ctx, t, cfg)
	}
	vaps, err := clientset.AdmissionregistrationV1().ValidatingAdmissionPolicies().List(ctx, metav1.ListOptions{})
	if err != nil {
		warnf(t, "cannot list the ValidatingAdmissionPolicies, check them by hand: %v", err)
		return ctx
	}
	for _, vap := range vaps.Items {
		warnf(t, "ValidatingAdmissionPolicy %s to review", vap.Name)
	}
	return ctx
}

func logKyvernoEnforced(ctx context.Context, t *testing.T, cfg *envconf.Config) {
	client, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "clusterpolicies"}
	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		warnf(t, "cannot list the Kyverno ClusterPolicies, check them by hand: %v", err)
		return
	}
	enforced := 0
	for _, policy := range list.Items {
		spec, _ := policy.Object["spec"].(map[string]any)
		action, _ := spec["validationFailureAction"].(string)
		isEnforced := strings.EqualFold(action, "enforce")
		rules, _ := spec["rules"].([]any)
		for _, rule := range rules {
			validate, _ := rule.(map[string]any)["validate"].(map[string]any)
			ruleAction, _ := validate["failureAction"].(string)
			isEnforced = isEnforced || strings.EqualFold(ruleAction, "enforce")
		}
		if isEnforced {
			enforced++
			warnf(t, "Kyverno ClusterPolicy %s enforces: check the test pods comply", policy.GetName())
		}
	}
	if enforced == 0 {
		t.Logf("no Kyverno ClusterPolicy in Enforce among %d", len(list.Items))
	}
}
