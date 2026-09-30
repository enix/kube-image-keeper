//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/enix/kube-image-keeper/test/utils"
)

// The routing specs check what only a real cluster answers: the API server calling the webhook the
// chart registers, probes crossing the cluster network to a registry, and the kubelet pulling the
// reference the webhook returned. The routing rules themselves are covered by the envtest suite of
// internal/webhook/core/v1.
//
// The registry that answers is registry.k8s.io, over HTTPS: an origin on a plain HTTP registry is
// always probed over HTTPS today, whatever the insecure flag of the entry it matches. Unreachable
// registries are .invalid hosts, which fail fast and the same way everywhere.

// routingNamespace holds the pods of the routing specs; only its label lets the resources apply
const routingNamespace = "kuik-e2e-routing"

// routingLabel is the namespace label the ImageAlternatives of the routing specs select
const routingLabel = "kuik.enix.io/e2e"

// pauseTag is the tag of every image the routing specs run: small, non-root, and served by
// registry.k8s.io
const pauseTag = "3.10"

// availableImage answers from registry.k8s.io
const availableImage = "registry.k8s.io/pause:" + pauseTag

// unreachableOrigin is an image whose registry does not resolve, with alternatives in pauseAlternative
const unreachableOrigin = "e2e-origin.invalid/pause:" + pauseTag

// exhaustedOrigin is an image whose registry does not resolve, and none of its alternatives either
const exhaustedOrigin = "e2e-unreachable.invalid/pause:" + pauseTag

// pauseAlternative offers registry.k8s.io behind an unreachable entry, so that routing skips a
// failing candidate before it retains one
const pauseAlternative = "e2e-pause"

// exhaustedAlternative offers only unreachable entries
const exhaustedAlternative = "e2e-unreachable"

// configMapName is the ConfigMap the chart mounts the global config file from
const configMapName = "kube-image-keeper-config"

// kuikAnnotationPrefix starts every annotation the webhook writes on a pod
const kuikAnnotationPrefix = "kuik.enix.io/"

// failingTimeout is a global config fragment under which no probe can answer in time
const failingTimeout = "webhook:\n  availabilityCheck:\n    timeout: 1ms\n"

// webhookPort and mutatePath are where each webhook pod serves the Pod admission
const (
	webhookPort = 9443
	mutatePath  = "/mutate--v1-pod"
)

var routingResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[1]s
spec:
  namespaceSelector:
    matchLabels:
      %[3]s: "true"
  alternatives:
    - repository: e2e-origin.invalid/pause
    - repository: e2e-down.invalid/pause
    - repository: registry.k8s.io/pause
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[2]s
spec:
  namespaceSelector:
    matchLabels:
      %[3]s: "true"
  alternatives:
    - repository: e2e-unreachable.invalid/pause
    - repository: e2e-unreachable-too.invalid/pause
`, pauseAlternative, exhaustedAlternative, routingLabel)

var _ = Describe("Pod routing", Ordered, func() {
	BeforeAll(func() {
		startAPIProxy()

		By("waiting for the webhook Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("webhook").Should(Succeed())

		By("creating the routing namespace, selected by the resources")
		_, err := kubectl("create", "ns", routingNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", routingNamespace, routingLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageAlternatives")
		Expect(kubectlStdin(routingResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())

		By("waiting until every webhook replica routes with the resources")
		Eventually(replicasRouting).Should(everyReplica(BeTrue()))
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "ns", routingNamespace, "--wait=false")
		_, _ = kubectl("delete", "imagealternatives", pauseAlternative, exhaustedAlternative, "--ignore-not-found")
	})

	Context("with an ImageAlternative whose original is unreachable", func() {
		It("starts the pod from the first alternative that answers", func() {
			createPod("fallback", unreachableOrigin)

			Expect(podImage("fallback")).To(Equal(availableImage))
			Eventually(podPhase).WithArguments("fallback").Should(Equal("Running"))
		})
	})

	Context("with an ImageAlternative whose original answers", func() {
		It("starts the pod with the image its author wrote and no kuik annotation", func() {
			createPod("original", availableImage)

			Expect(podImage("original")).To(Equal(availableImage))
			Expect(kuikAnnotations("original")).To(BeEmpty())
			Eventually(podPhase).WithArguments("original").Should(Equal("Running"))
		})
	})

	Context("when no candidate answers", func() {
		It("admits the pod untouched, naming the resource in kuik.enix.io/no-alternatives", func() {
			createPod("exhausted", exhaustedOrigin)

			Expect(podImage("exhausted")).To(Equal(exhaustedOrigin))
			Expect(kuikAnnotations("exhausted")).To(Equal(map[string]string{
				"kuik.enix.io/no-alternatives": `{"pause":["ImageAlternative/` + exhaustedAlternative + `"]}`,
			}))
		})
	})

	Context("when the webhook is unavailable", func() {
		It("admits the pod with the image its author wrote", func() {
			By("scaling the webhook down to no replica")
			scaleWebhook(0)
			DeferCleanup(func() {
				scaleWebhook(2)
				Eventually(webhookPods).Should(HaveLen(2))
				Eventually(deploymentAvailable).WithArguments("webhook").Should(Succeed())
				Eventually(replicasRouting).Should(everyReplica(BeTrue()))
			})
			Eventually(webhookPods).Should(BeEmpty())

			By("creating a pod the webhook would have rewritten")
			createPod("unavailable", unreachableOrigin)

			Expect(podImage("unavailable")).To(Equal(unreachableOrigin))
			Expect(kuikAnnotations("unavailable")).To(BeEmpty())
		})
	})

	Context("when the global config ConfigMap is edited", func() {
		var original string

		BeforeEach(func() {
			var err error
			original, err = kubectl("get", "configmap", configMapName, "-n", namespace,
				"-o", `jsonpath={.data.config\.yaml}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(original).NotTo(BeEmpty())
			original = strings.TrimRight(original, "\n") + "\n"
			DeferCleanup(setConfig, original)
		})

		// A 1ms availabilityCheck.timeout fails every probe, so a replica that loaded it stops
		// rewriting: routing tells which config each replica holds, with no timing involved. The
		// kubelet refreshes a ConfigMap volume on its sync period, up to a minute or more, and
		// the check cache keeps earlier answers for its TTL.
		It("applies a valid edit without a restart", func() {
			before := webhookRestarts()

			setConfig(original + failingTimeout)

			Eventually(replicasRouting, "3m").Should(everyReplica(BeFalse()))
			Expect(webhookRestarts()).To(Equal(before))
		})

		It("keeps the previously loaded config when the edited file does not validate, and counts the failure", func() {
			By("loading a config that differs from the defaults")
			setConfig(original + failingTimeout)
			Eventually(replicasRouting, "3m").Should(everyReplica(BeFalse()))
			before := webhookRestarts()
			errorsBefore, err := webhookMetric("kuik_config_reload_errors_total")
			Expect(err).NotTo(HaveOccurred())

			setConfig(original + "webhook:\n  availabilityCheck:\n    timeout: banana\n")

			for pod, count := range errorsBefore {
				Eventually(func() (float64, error) {
					counts, err := webhookMetric("kuik_config_reload_errors_total")
					return counts[pod], err
				}, "3m").Should(BeNumerically(">", count), "the webhook pod %s did not count the rejected reload", pod)
			}
			Expect(webhookRestarts()).To(Equal(before))
			Expect(replicasRouting()).To(everyReplica(BeFalse()), "a replica left the previously loaded config")
		})
	})
})

// podManifest is a pod of the routing namespace running image in one container named pause,
// admissible under the restricted security policy.
func podManifest(name, image string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
    - name: pause
      image: %s
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        runAsNonRoot: true
        runAsUser: 65535
        seccompProfile:
          type: RuntimeDefault
`, name, routingNamespace, image)
}

// createPod creates a pod of the routing namespace, going through admission once.
func createPod(name, image string) {
	GinkgoHelper()
	Expect(kubectlStdin(podManifest(name, image), "create", "-f", "-")).Error().NotTo(HaveOccurred())
}

// kubectlStdin runs kubectl with args, feeding it input on stdin, bounded by kubectlTimeout.
func kubectlStdin(input string, args ...string) (string, error) {
	return runBounded(kubectlTimeout, strings.NewReader(input), "kubectl", args...)
}

// podImage is the image of the container of a pod of the routing namespace, as admitted.
func podImage(name string) (string, error) {
	return kubectl("get", "pod", name, "-n", routingNamespace, "-o", "jsonpath={.spec.containers[0].image}")
}

// podPhase is the phase of a pod of the routing namespace.
func podPhase(name string) (string, error) {
	return kubectl("get", "pod", name, "-n", routingNamespace, "-o", "jsonpath={.status.phase}")
}

// kuikAnnotations are the annotations of a pod of the routing namespace the webhook wrote.
func kuikAnnotations(name string) (map[string]string, error) {
	output, err := kubectl("get", "pod", name, "-n", routingNamespace, "-o", "jsonpath={.metadata.annotations}")
	if err != nil {
		return nil, err
	}
	annotations := map[string]string{}
	if output != "" {
		if err := json.Unmarshal([]byte(output), &annotations); err != nil {
			return nil, err
		}
	}
	kuik := map[string]string{}
	for key, value := range annotations {
		if strings.HasPrefix(key, kuikAnnotationPrefix) {
			kuik[key] = value
		}
	}
	return kuik, nil
}

// deploymentAvailable fails until the Deployment of process reports Available.
func deploymentAvailable(g Gomega, process string) {
	output, err := kubectl("get", "deployment", deploymentName(process), "-n", namespace,
		"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).To(Equal("True"))
}

// scaleWebhook sets the replica count of the webhook Deployment.
func scaleWebhook(replicas int) {
	GinkgoHelper()
	_, err := kubectl("scale", "deployment", deploymentName("webhook"), "-n", namespace,
		"--replicas="+strconv.Itoa(replicas))
	Expect(err).NotTo(HaveOccurred())
}

// webhookPods are the names of the webhook pods, terminating ones included.
func webhookPods() ([]string, error) {
	output, err := kubectl("get", "pods", "-l", processSelector("webhook"), "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	return strings.Fields(output), err
}

// webhookRestarts maps each webhook pod to the restart count of its container: a pod replaced
// or restarted changes the map.
func webhookRestarts() map[string]string {
	GinkgoHelper()
	output, err := kubectl("get", "pods", "-l", processSelector("webhook"), "-n", namespace,
		"-o", `go-template={{ range .items }}{{ .metadata.name }} {{ range .status.containerStatuses }}`+
			`{{ .restartCount }}{{ end }}{{ "\n" }}{{ end }}`)
	Expect(err).NotTo(HaveOccurred())
	restarts := map[string]string{}
	for _, line := range utils.GetNonEmptyLines(output) {
		pod, count, _ := strings.Cut(line, " ")
		restarts[pod] = count
	}
	Expect(restarts).NotTo(BeEmpty())
	return restarts
}

// setConfig replaces the global config file in the ConfigMap the chart mounts.
func setConfig(content string) {
	GinkgoHelper()
	patch, err := json.Marshal(map[string]any{"data": map[string]string{"config.yaml": content}})
	Expect(err).NotTo(HaveOccurred())
	_, err = kubectl("patch", "configmap", configMapName, "-n", namespace, "--type=merge", "-p", string(patch))
	Expect(err).NotTo(HaveOccurred())
}

// webhookMetric reads an unlabelled counter on each webhook pod: each replica counts its own.
func webhookMetric(metric string) (map[string]float64, error) {
	pods, err := webhookPods()
	if err != nil {
		return nil, err
	}
	counts := map[string]float64{}
	for _, pod := range pods {
		output, err := kubectl("get", "--raw",
			fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/metrics", namespace, pod, metricsPort))
		if err != nil {
			return nil, err
		}
		for line := range strings.SplitSeq(output, "\n") {
			value, found := strings.CutPrefix(line, metric+" ")
			if !found {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return nil, err
			}
			counts[pod] = v
		}
	}
	return counts, nil
}

// replicasRouting tells, for each webhook pod, whether it rewrites a pod of unreachableOrigin.
// It sends the AdmissionReview to each pod through the API server's proxy: admission itself
// reaches whichever replica the API server's connection is bound to, so it cannot tell whether
// every replica holds the same resources and config.
func replicasRouting() (map[string]bool, error) {
	pods, err := webhookPods()
	if err != nil {
		return nil, err
	}
	object, err := yaml.YAMLToJSON([]byte(podManifest("replica-probe", unreachableOrigin)))
	if err != nil {
		return nil, err
	}
	review, err := json.Marshal(admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request: &admissionv1.AdmissionRequest{
			UID:       "e2e-replica-probe",
			Kind:      metav1.GroupVersionKind{Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Version: "v1", Resource: "pods"},
			Namespace: routingNamespace,
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: object},
		},
	})
	if err != nil {
		return nil, err
	}
	routing := map[string]bool{}
	for _, pod := range pods {
		// kubectl create --raw sends no Content-Type, which the webhook server refuses.
		url := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/https:%s:%d/proxy%s",
			apiProxy, namespace, pod, webhookPort, mutatePath)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(review))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := proxyClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		output := string(body)
		var answer admissionv1.AdmissionReview
		if err := json.Unmarshal([]byte(output), &answer); err != nil || answer.Response == nil {
			return nil, fmt.Errorf("decoding the answer of %s: %w (%s)", pod, err, output)
		}
		if !answer.Response.Allowed || answer.Response.Result != nil && answer.Response.Result.Code >= 400 {
			return nil, fmt.Errorf("the webhook pod %s did not process the review: %s", pod, output)
		}
		routing[pod] = strings.Contains(string(answer.Response.Patch), availableImage)
	}
	return routing, nil
}

// apiProxy is the base URL of the kubectl proxy the routing specs reach the API server through,
// set by startAPIProxy
var apiProxy string

// proxyClient bounds each request through apiProxy: Eventually never interrupts a poll that
// hangs, so a replica or a proxy that accepts the connection and never answers would stall the
// suite
var proxyClient = &http.Client{Timeout: 10 * time.Second}

// startAPIProxy runs kubectl proxy on a free local port until the end of the running container,
// and sets apiProxy to it.
func startAPIProxy() {
	GinkgoHelper()
	cmd := exec.Command("kubectl", "proxy", "--port=0")
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// kubectl prints "Starting to serve on 127.0.0.1:<port>" once it listens. The read is bounded:
	// a proxy that never starts would otherwise block it forever.
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
	}()
	var line string
	Eventually(lines).WithTimeout(kubectlTimeout).Should(Receive(&line), "kubectl proxy did not start")
	address, found := strings.CutPrefix(strings.TrimSpace(line), "Starting to serve on ")
	Expect(found).To(BeTrue(), "unexpected kubectl proxy output: %q", line)
	apiProxy = "http://" + address
}

// everyReplica matches a replicasRouting result where each of the 2 webhook replicas matches
// matcher.
func everyReplica(matcher OmegaMatcher) OmegaMatcher {
	return And(HaveLen(2), HaveEach(matcher))
}
