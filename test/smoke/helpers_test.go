//go:build smoke

package smoke

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

const (
	nginx = "quay.io/nginx/nginx-unprivileged:1.31.6-alpine"
	// unresolvable never resolves: .invalid is reserved (RFC 2606).
	unresolvable = "kuik-test.invalid/nginx/nginx-unprivileged:1.31.6-alpine"
)

// testPod is a pod that runs under the restricted Pod Security profile, so a shared
// cluster's admission policies let it in.
func testPod(namespace, name, image string, podLabels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: podLabels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "nginx",
				Image: image,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("16Mi"),
					},
					Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             new(true),
					AllowPrivilegeEscalation: new(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
			}},
		},
	}
}

// waitFor polls cond every 3 s for at most timeout.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func(ctx context.Context) (bool, error)) {
	t.Helper()
	err := wait.For(cond, wait.WithTimeout(timeout), wait.WithInterval(3*time.Second), wait.WithImmediate())
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func waitPodReady(t *testing.T, cfg *envconf.Config, pod *corev1.Pod) {
	t.Helper()
	cond := conditions.New(cfg.Client().Resources()).PodReady(pod)
	if err := wait.For(cond, wait.WithTimeout(150*time.Second), wait.WithInterval(3*time.Second)); err != nil {
		t.Fatalf("pod %s is not Ready: %v%s", pod.Name, err, webhookLogs(context.Background(), pod.Name))
	}
}

// getPod reads pod back: create does not refresh the object with what the webhook changed.
func getPod(ctx context.Context, t *testing.T, cfg *envconf.Config, namespace, name string) *corev1.Pod {
	t.Helper()
	var pod corev1.Pod
	if err := cfg.Client().Resources().Get(ctx, name, namespace, &pod); err != nil {
		t.Fatalf("cannot read pod %s: %v", name, err)
	}
	return &pod
}

// rewritesTotal sums kuik_routing_rewrites_total of the resource over every webhook replica:
// each replica counts only the admissions it served.
func rewritesTotal(ctx context.Context, t *testing.T, name string) float64 {
	t.Helper()
	pods, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=kube-image-keeper,app.kubernetes.io/component=webhook",
	})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("cannot find the webhook pods: %v", err)
	}
	var total float64
	for _, pod := range pods.Items {
		body, err := clientset.CoreV1().Pods(*kuikNamespace).ProxyGet("", pod.Name, "8080", "metrics", nil).DoRaw(ctx)
		if err != nil {
			t.Fatalf("cannot read the metrics of %s: %v", pod.Name, err)
		}
		total += sumSeries(body, "kuik_routing_rewrites_total", fmt.Sprintf("name=%q", name))
	}
	return total
}

// sumSeries adds the values of the series of metric whose labels contain label.
func sumSeries(exposition []byte, metric, label string) float64 {
	var sum float64
	scanner := bufio.NewScanner(bytes.NewReader(exposition))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, metric+"{") || !strings.Contains(line, label) {
			continue
		}
		fields := strings.Fields(line)
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			sum += v
		}
	}
	return sum
}

func podEvent(ctx context.Context, namespace, pod, reason string) (bool, error) {
	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,reason=%s", pod, reason),
	})
	if err != nil {
		return false, err
	}
	return len(events.Items) > 0, nil
}

// resourceEvent tells whether a cluster-scoped resource got an event with reason: kuik emits
// those through the events.k8s.io API, outside any namespace.
func resourceEvent(ctx context.Context, name, reason string) (bool, error) {
	events, err := clientset.EventsV1().Events("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("reason=%s,regarding.name=%s", reason, name),
	})
	if err != nil {
		return false, err
	}
	return len(events.Items) > 0, nil
}

// webhookLogs returns the webhook log lines naming pod, for a failure message.
func webhookLogs(ctx context.Context, pod string) string {
	pods, err := clientset.CoreV1().Pods(*kuikNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=kube-image-keeper,app.kubernetes.io/component=webhook",
	})
	if err != nil {
		return ""
	}
	var out strings.Builder
	for _, p := range pods.Items {
		options := &corev1.PodLogOptions{SinceSeconds: ptr.To[int64](900)}
		logs, err := clientset.CoreV1().Pods(*kuikNamespace).GetLogs(p.Name, options).DoRaw(ctx)
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(logs)) {
			if strings.Contains(line, strconv.Quote(pod)) {
				fmt.Fprintf(&out, "[%s] %s", p.Name, line)
			}
		}
	}
	if out.Len() == 0 {
		return ""
	}
	return "\n--- webhook log lines naming pod " + pod + ":\n" + out.String()
}
