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

// demotedOrigin answers from registry.k8s.io, written with its port so that its registry has a
// pacing block of its own: a check timeout set there fails the monitor's checks of it alone,
// while the webhook, bound by its own timeout, still finds it available
const demotedOrigin = "registry.k8s.io:443/pause:" + pauseTag

// demoteUnreachable is an image whose registry does not resolve, offered the alternatives of
// demotedOrigin: a replica that rewrites it has loaded the ImageAlternative of demoteName
const demoteUnreachable = "e2e-demote.invalid/pause:" + pauseTag

// demoteName names the ImageAlternative offering availableImage for demotedOrigin, and the
// ImageMonitor tracking demotedOrigin
const demoteName = "e2e-demote"

// demoteNamespace holds the pod of demotedOrigin the monitor tracks, and only the monitor
// selects it
const demoteNamespace = "kuik-e2e-demote"

// demoteLabel is the namespace label the ImageMonitor of demoteName selects
const demoteLabel = "kuik.enix.io/e2e-demote"

// failingOriginChecks is a global config fragment under which no background check of the
// registry of demotedOrigin answers in time, checked every 5s so the monitor reports it within
// seconds of the reload; the webhook's probe is not bound by it
const failingOriginChecks = "registries:\n  \"registry.k8s.io:443\":\n" +
	"    check:\n      interval: 5s\n      timeout: 1ms\n"

var demoteResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[1]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: e2e-demote.invalid/pause
    - repository: registry.k8s.io/pause
    - repository: registry.k8s.io:443/pause
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: %[1]s
spec:
  namespaceSelector:
    matchLabels:
      %[3]s: "true"
`, demoteName, routingLabel, demoteLabel)

// alternativeOrigin is an image whose registry does not resolve, offered demotedAlternative
// then availableImage
const alternativeOrigin = "e2e-demote-alternative.invalid/pause:" + pauseTag

// demotedAlternative answers from k8s.gcr.io, which redirects to registry.k8s.io: its registry
// has a pacing block of its own, so a check timeout set there fails the monitor's checks of it
// alone, while the webhook, bound by its own timeout, still finds it available
const demotedAlternative = "k8s.gcr.io/pause:" + pauseTag

// demoteAlternativeName names the ImageAlternative offering the alternatives of
// alternativeOrigin, and the ImageMonitor monitoring them
const demoteAlternativeName = "e2e-demote-alternative"

// demoteAlternativeNamespace holds the pod of alternativeOrigin the monitor tracks; the
// ImageAlternative selects it as it does the routing namespace, the monitor selects it alone
const demoteAlternativeNamespace = "kuik-e2e-demote-alternative"

// demoteAlternativeLabel is the namespace label the ImageMonitor of demoteAlternativeName selects
const demoteAlternativeLabel = "kuik.enix.io/e2e-demote-alternative"

// failingAlternativeChecks is a global config fragment under which no background check of the
// registry of demotedAlternative answers in time, checked every 5s so the monitor reports it
// within seconds of the reload; the webhook's probe is not bound by it
const failingAlternativeChecks = "registries:\n  k8s.gcr.io:\n    check:\n      interval: 5s\n      timeout: 1ms\n"

var demoteAlternativeResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[1]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: e2e-demote-alternative.invalid/pause
    - repository: k8s.gcr.io/pause
    - repository: registry.k8s.io/pause
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: %[1]s
spec:
  monitorAlternatives: true
  namespaceSelector:
    matchLabels:
      %[3]s: "true"
`, demoteAlternativeName, routingLabel, demoteAlternativeLabel)

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

	Context("with an ImageMonitor reporting the original unavailable", func() {
		// The webhook reads the monitor's status through its informers: only a real reconciler
		// writes it, from checks that really fail, and a pod admitted by either replica tells
		// the order it probed in. Both candidates answer the webhook, so the original is served
		// unless it is demoted.
		It("serves the alternative while an ImageMonitor lists the original unavailable", func() {
			By("tracking the original with an ImageMonitor, in a namespace no routing resource selects")
			_, err := kubectl("create", "ns", demoteNamespace)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "ns", demoteNamespace, "--wait=false")
			_, err = kubectl("label", "ns", demoteNamespace, demoteLabel+"=true")
			Expect(err).NotTo(HaveOccurred())
			Expect(kubectlStdin(demoteResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "imagealternative,imagemonitor", demoteName, "--ignore-not-found")
			Expect(kubectlStdin(pausePodManifest(demoteNamespace, "tracked", demotedOrigin), "create", "-f", "-")).
				Error().NotTo(HaveOccurred())

			original, err := kubectl("get", "configmap", configMapName, "-n", namespace, "-o", `jsonpath={.data.config\.yaml}`)
			Expect(err).NotTo(HaveOccurred())
			// The fragment is the only pace of registry.k8s.io:443: restoring the original drops it.
			Expect(original).NotTo(ContainSubstring("registry.k8s.io:443"))
			original = strings.TrimRight(original, "\n") + "\n"
			// The restore needs no wait: the fragment only paces registry.k8s.io:443, which no
			// other spec reads, and the monitor that checks it is deleted.
			DeferCleanup(setConfig, original)

			By("waiting until every webhook replica routes with the ImageAlternative")
			Eventually(replicasRewriting).WithTimeout(30 * time.Second).WithArguments(demoteUnreachable).
				Should(everyReplica(BeTrue()))

			By("checking that every replica keeps the original, which answers")
			// The first probe of a cold host (TLS, token, redirect) can exceed the webhook timeout.
			Eventually(replicasRewriting).WithTimeout(30 * time.Second).WithArguments(demotedOrigin).
				Should(everyReplica(BeFalse()))
			createPod("demote-kept", demotedOrigin)
			Expect(podImage("demote-kept")).To(Equal(demotedOrigin))
			Expect(kuikAnnotations("demote-kept")).To(BeEmpty())

			By("failing the monitor's checks of the original's registry")
			setConfig(mergeConfig(original, failingOriginChecks))
			// The kubelet syncs the ConfigMap volume on its sync period (10s on the e2e cluster,
			// see kind-config.yaml), then the ring checks the original on its next window, 10s
			// apart on the e2e release (see values.yaml).
			Eventually(kubectl).WithTimeout(4*time.Minute).WithArguments("get", "imagemonitor", demoteName, "-o",
				fmt.Sprintf(`jsonpath={.status.unavailableImages[?(@.ref==%q)].ref}`, demotedOrigin)).
				Should(Equal(demotedOrigin))

			// Each replica's informer may see the status a moment after the API server does.
			Eventually(replicasRewriting).WithTimeout(30 * time.Second).WithArguments(demotedOrigin).
				Should(everyReplica(BeTrue()))
			createPod("demote-served", demotedOrigin)
			Expect(podImage("demote-served")).To(Equal(availableImage))
			Expect(kuikAnnotations("demote-served")).To(HaveKeyWithValue("kuik.enix.io/rewrites", MatchJSON(fmt.Sprintf(
				`{"pause":{"by":"ImageAlternative/%s","origin":%q,"rewrittenTo":%q,"policy":"OnFailure"}}`,
				demoteName, demotedOrigin, availableImage))))
		})
	})

	Context("with an ImageMonitor reporting an alternative unavailable", func() {
		// Only a real reconciler, monitoring alternatives, writes unavailableAlternatives, from
		// checks that really fail. The original never answers and both alternatives answer the
		// webhook, so the first alternative is served unless it is demoted.
		It("serves the next alternative while an ImageMonitor lists the first one unavailable", func() {
			By("tracking the original with an ImageMonitor monitoring its alternatives")
			_, err := kubectl("create", "ns", demoteAlternativeNamespace)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "ns", demoteAlternativeNamespace, "--wait=false")
			_, err = kubectl("label", "ns", demoteAlternativeNamespace, routingLabel+"=true", demoteAlternativeLabel+"=true")
			Expect(err).NotTo(HaveOccurred())
			Expect(kubectlStdin(demoteAlternativeResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "imagealternative,imagemonitor", demoteAlternativeName, "--ignore-not-found")

			original, err := kubectl("get", "configmap", configMapName, "-n", namespace, "-o", `jsonpath={.data.config\.yaml}`)
			Expect(err).NotTo(HaveOccurred())
			// The fragment is the only pace of k8s.gcr.io: restoring the original drops it.
			Expect(original).NotTo(ContainSubstring("k8s.gcr.io"))
			original = strings.TrimRight(original, "\n") + "\n"
			// The restore needs no wait: the fragment only paces k8s.gcr.io, which no other spec
			// reads, and the monitor that checks it is deleted.
			DeferCleanup(setConfig, original)

			// rewrittenTo is the kuik.enix.io/rewrites value of a pod of alternativeOrigin served target.
			rewrittenTo := func(target string) string {
				return fmt.Sprintf(`{"pause":{"by":"ImageAlternative/%s","origin":%q,"rewrittenTo":%q,"policy":"OnFailure"}}`,
					demoteAlternativeName, alternativeOrigin, target)
			}

			By("waiting until every webhook replica serves the first alternative")
			Eventually(replicasRewritingTo).WithTimeout(30*time.Second).
				WithArguments(alternativeOrigin, demotedAlternative).Should(everyReplica(BeTrue()))
			// The webhook rewrites the tracked pod: the monitor tracks its origin and the
			// alternatives offered for it.
			tracked := pausePodManifest(demoteAlternativeNamespace, "tracked", alternativeOrigin)
			Expect(kubectlStdin(tracked, "create", "-f", "-")).Error().NotTo(HaveOccurred())
			createPod("demote-alternative-kept", alternativeOrigin)
			Expect(podImage("demote-alternative-kept")).To(Equal(demotedAlternative))
			Expect(kuikAnnotations("demote-alternative-kept")).
				To(HaveKeyWithValue("kuik.enix.io/rewrites", MatchJSON(rewrittenTo(demotedAlternative))))

			By("failing the monitor's checks of the first alternative's registry")
			setConfig(mergeConfig(original, failingAlternativeChecks))
			// The kubelet syncs the ConfigMap volume on its sync period (10s on the e2e cluster,
			// see kind-config.yaml), then the ring checks the alternative on its next window, 5s
			// apart for k8s.gcr.io.
			via := fmt.Sprintf(`jsonpath={range .status.unavailableAlternatives[?(@.ref==%q)]}{.via}{end}`,
				demotedAlternative)
			Eventually(kubectl).WithTimeout(4*time.Minute).
				WithArguments("get", "imagemonitor", demoteAlternativeName, "-o", via).
				Should(Equal("ImageAlternative/" + demoteAlternativeName))

			// Each replica's informer may see the status a moment after the API server does.
			Eventually(replicasRewriting).WithTimeout(30 * time.Second).WithArguments(alternativeOrigin).
				Should(everyReplica(BeTrue()))
			createPod("demote-alternative-served", alternativeOrigin)
			Expect(podImage("demote-alternative-served")).To(Equal(availableImage))
			Expect(kuikAnnotations("demote-alternative-served")).
				To(HaveKeyWithValue("kuik.enix.io/rewrites", MatchJSON(rewrittenTo(availableImage))))
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
		// kubelet refreshes a ConfigMap volume on its sync period (10s on the e2e cluster, see
		// kind-config.yaml), and the check cache keeps earlier answers for its TTL.
		It("applies a valid edit without a restart", func() {
			before := webhookRestarts()

			setConfig(mergeConfig(original, failingTimeout))

			Eventually(replicasRouting, "3m").Should(everyReplica(BeFalse()))
			Expect(webhookRestarts()).To(Equal(before))
		})

		It("keeps the previously loaded config when the edited file does not validate, and counts the failure", func() {
			By("loading a config that differs from the defaults")
			setConfig(mergeConfig(original, failingTimeout))
			Eventually(replicasRouting, "3m").Should(everyReplica(BeFalse()))
			before := webhookRestarts()
			errorsBefore, err := webhookMetric("kuik_config_reload_errors_total")
			Expect(err).NotTo(HaveOccurred())

			setConfig(mergeConfig(original, "webhook:\n  availabilityCheck:\n    timeout: banana\n"))

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

// mergeConfig returns the global config file base with the YAML fragment merged in, key by key:
// a mapping of the fragment merges into the mapping base holds under the same key, any other
// value replaces it. Appending the fragment would repeat a top-level key the base already has
// (registries, set by the e2e values), which the config loader refuses.
func mergeConfig(base, fragment string) string {
	GinkgoHelper()
	var merged, patch map[string]any
	Expect(yaml.Unmarshal([]byte(base), &merged)).To(Succeed())
	Expect(yaml.Unmarshal([]byte(fragment), &patch)).To(Succeed())
	if merged == nil {
		merged = map[string]any{}
	}
	mergeMappings(merged, patch)
	content, err := yaml.Marshal(merged)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// mergeMappings merges patch into dst, recursing into the mappings both hold under one key.
func mergeMappings(dst, patch map[string]any) {
	for key, value := range patch {
		into, isMapping := dst[key].(map[string]any)
		from, fromMapping := value.(map[string]any)
		if isMapping && fromMapping {
			mergeMappings(into, from)
			continue
		}
		dst[key] = value
	}
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
func replicasRouting() (map[string]bool, error) {
	return replicasRewriting(unreachableOrigin)
}

// replicasRewriting tells, for each webhook pod, whether it rewrites a pod of image to
// availableImage.
func replicasRewriting(image string) (map[string]bool, error) {
	return replicasRewritingTo(image, availableImage)
}

// replicasRewritingTo tells, for each webhook pod, whether it rewrites a pod of image to
// target. It sends the AdmissionReview to each pod through the API server's proxy: admission
// itself reaches whichever replica the API server's connection is bound to, so it cannot tell
// whether every replica holds the same resources and config.
func replicasRewritingTo(image, target string) (map[string]bool, error) {
	pods, err := webhookPods()
	if err != nil {
		return nil, err
	}
	object, err := yaml.YAMLToJSON([]byte(podManifest("replica-probe", image)))
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
		routing[pod] = strings.Contains(string(answer.Response.Patch), target)
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
