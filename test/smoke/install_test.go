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

	. "github.com/onsi/gomega"
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
	g := NewWithT(t)
	out, err := helm(ctx, cfg, "list", "-o", "json")
	g.Expect(err).NotTo(HaveOccurred(), "list the Helm releases of %s", *kuikNamespace)
	var releases []struct {
		Name       string `json:"name"`
		Chart      string `json:"chart"`
		AppVersion string `json:"app_version"`
		Status     string `json:"status"`
	}
	g.Expect(json.Unmarshal(out, &releases)).To(Succeed())
	g.Expect(releases).To(ContainElement(HaveField("Name", *release), &releases),
		"Helm release %s in %s", *release, *kuikNamespace)
	r := releases[0]
	g.Expect(r.Status).To(Equal("deployed"), "status of release %s", r.Name)
	if *kuikVersion != "" {
		g.Expect(r.Chart).To(Equal("kube-image-keeper-"+*kuikVersion), "chart of release %s", r.Name)
		g.Expect(r.AppVersion).To(Equal(*kuikVersion), "app version of release %s", r.Name)
	}
	t.Logf("release %s: chart %s, app %s, %s", r.Name, r.Chart, r.AppVersion, r.Status)
	return ctx
}

// logVerbosity logs the Helm values verbosity as they are, empty included (an empty process
// value inherits the root one), and the level each process runs at: the skill restores the
// former after the test and asks for debug when the latter is lower.
func logVerbosity(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	g := NewWithT(t)
	out, err := helm(ctx, cfg, "get", "values", *release, "--all", "-o", "json")
	g.Expect(err).NotTo(HaveOccurred(), "read the Helm values of %s", *release)
	var values struct {
		Verbosity    string                     `json:"verbosity"`
		Webhook      struct{ Verbosity string } `json:"webhook"`
		Reconciler   struct{ Verbosity string } `json:"reconciler"`
		SecretSyncer struct{ Verbosity string } `json:"secretSyncer"`
	}
	g.Expect(json.Unmarshal(out, &values)).To(Succeed())
	t.Logf("Helm verbosity: root %q, webhook %q, reconciler %q, secretSyncer %q",
		values.Verbosity, values.Webhook.Verbosity, values.Reconciler.Verbosity, values.SecretSyncer.Verbosity)

	fullname := *release
	if !strings.Contains(fullname, "kube-image-keeper") {
		fullname += "-kube-image-keeper"
	}
	for _, process := range []string{"webhook", "reconciler", "secret-syncer"} {
		deploy, err := clientset.AppsV1().Deployments(*kuikNamespace).Get(ctx, fullname+"-"+process, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "read the Deployment of the %s", process)
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
	g := NewWithT(t)
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: kuikPods})
	g.Expect(err).NotTo(HaveOccurred(), "list the kuik pods")
	g.Expect(list.Items).NotTo(BeEmpty(), "no kuik pod in %s", *kuikNamespace)
	for _, pod := range list.Items {
		g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "phase of %s", pod.Name)
		g.Expect(pod.Status.ContainerStatuses).To(HaveEach(HaveField("RestartCount", BeZero())),
			"restarts of %s", pod.Name)
	}
	t.Logf("%d kuik pods Running, none restarted", len(list.Items))
	return ctx
}

func expectNoErrorLogs(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	g := NewWithT(t)
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: kuikPods})
	g.Expect(err).NotTo(HaveOccurred(), "list the kuik pods")
	for _, pod := range list.Items {
		logs, err := clientset.CoreV1().Pods(*kuikNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
		g.Expect(err).NotTo(HaveOccurred(), "read the logs of %s", pod.Name)
		var errors []string
		for line := range strings.Lines(string(logs)) {
			if errorLine.MatchString(line) {
				errors = append(errors, strings.TrimSpace(line))
			}
		}
		g.Expect(errors).To(BeEmpty(), "error lines in the logs of %s", pod.Name)
	}
	return ctx
}

// expectBuildInfo checks kuik_build_info on every webhook replica: the version, and the
// revision against the commit of tag v<version> when the tag is known locally.
func expectBuildInfo(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	g := NewWithT(t)
	list, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{LabelSelector: webhookPods})
	g.Expect(err).NotTo(HaveOccurred(), "list the webhook pods")
	g.Expect(list.Items).NotTo(BeEmpty(), "no webhook pod in %s", *kuikNamespace)
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
		g.Expect(err).NotTo(HaveOccurred(), "read the metrics of %s", pod.Name)
		info := ""
		for line := range strings.Lines(string(body)) {
			if strings.HasPrefix(line, "kuik_build_info{") {
				info = strings.TrimSpace(line)
			}
		}
		g.Expect(info).NotTo(BeEmpty(), "kuik_build_info of %s", pod.Name)
		if *kuikVersion != "" {
			g.Expect(info).To(ContainSubstring(fmt.Sprintf("version=%q", *kuikVersion)), "version of %s", pod.Name)
		}
		if revision != "" {
			g.Expect(info).To(ContainSubstring(fmt.Sprintf("revision=%q", revision)), "revision of %s", pod.Name)
		}
		t.Logf("%s: %s", pod.Name, info)
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
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
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
