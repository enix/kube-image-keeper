//go:build e2e

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// monitorNamespace holds the pods of the monitoring specs; its label is what only
// monitorName selects.
const monitorNamespace = "kuik-e2e-monitor"

// monitorLabel is the namespace label monitorName selects.
const monitorLabel = "kuik.enix.io/e2e-monitor"

// monitorName is the ImageMonitor under test.
const monitorName = "e2e-monitor"

var monitorManifest = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: %s
spec:
  namespaceSelector:
    matchLabels:
      %s: "true"
`, monitorName, monitorLabel)

var _ = Describe("Image monitoring", Ordered, func() {
	BeforeAll(func() {
		By("waiting for the reconciler Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())

		By("creating the monitoring namespace, selected by the monitor")
		_, err := kubectl("create", "ns", monitorNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", monitorNamespace, monitorLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageMonitor and creating a pod it selects")
		Expect(kubectlStdin(monitorManifest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Expect(kubectlStdin(monitorPodManifest("monitored", availableImage), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "ns", monitorNamespace, "--wait=false")
		_, _ = kubectl("delete", "imagemonitor", monitorName, "--ignore-not-found")
	})

	It("reports the image of a pod it selects as running in its status", func() {
		Eventually(kubectl).WithArguments("get", "imagemonitor", monitorName,
			"-o", "jsonpath={.status.images.origin.running} {.status.images.origin.tracked}").
			Should(Equal("1 1"))
	})

	It("exports that image in kuik_images_tracked on the reconciler holding the lease", func() {
		series := fmt.Sprintf(`kuik_images_tracked{kind="ImageMonitor",name=%q,reference="origin",state="running"}`,
			monitorName)
		// Only the leader reconciles: the series exists on one replica, so the sum over the
		// replicas is its value.
		Eventually(reconcilerSeries).WithArguments(series).Should(Equal(1.0))
	})

	It("reports an image whose registry does not answer in unavailableImages and kuik_image_origin_unavailable", func() {
		// A host that fails at DNS: the check is Unreachable at once.
		const unreachable = "e2e-monitor.invalid/pause:" + pauseTag
		Expect(kubectlStdin(monitorPodManifest("unreachable", unreachable), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())

		// The reconciler checks it on its own windows, one minute apart by default: no reconcile
		// is asked for.
		Eventually(kubectl).WithTimeout(4*time.Minute).WithArguments("get", "imagemonitor", monitorName,
			"-o", fmt.Sprintf(`jsonpath={.status.unavailableImages[?(@.ref==%q)].reason}`, unreachable)).
			Should(Equal("Unreachable"))
		series := fmt.Sprintf(`kuik_image_origin_unavailable{image=%q,kind="ImageMonitor",name=%q,`+
			`reason="Unreachable",registry="e2e-monitor.invalid"}`, unreachable, monitorName)
		Eventually(reconcilerSeries).WithArguments(series).Should(Equal(1.0))
	})

	It("reports no drift for a multi-arch tag the kubelet pulled by tag", func() {
		// The node's runtime reports the digest it pulled; only a real kubelet can say whether it
		// is the digest the registry serves for the tag (kubernetes/kubernetes#115199). The pause
		// tag of availableImage is the node's sandbox image, already present and reported as a
		// bare config digest: another tag of it is pulled for real.
		const pulled = "registry.k8s.io/pause:3.9"
		Expect(kubectlStdin(monitorPodManifest("pulled", pulled), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "pod", "pulled", "-n", monitorNamespace,
			"-o", "jsonpath={.status.containerStatuses[0].imageID}").Should(ContainSubstring("@sha256:"))
		// Both pause tags answer; the unreachable image of the previous spec does not.
		Eventually(kubectl).WithTimeout(4*time.Minute).WithArguments("get", "imagemonitor", monitorName,
			"-o", "jsonpath={.status.images.origin.available}").Should(Equal("2"))
		// Both the upstream digest and the running one are known: a report holding them follows
		// within the status interval. Only the pulled tag is asserted: the sandbox tag takes the
		// platform digest of the mirror specs once they pulled it by digest, which the naive
		// comparison reports as a drift.
		Consistently(kubectl).WithTimeout(30*time.Second).WithArguments("get", "imagemonitor", monitorName,
			"-o", fmt.Sprintf(`jsonpath={.status.driftedImages[?(@.ref==%q)].ref}`, pulled)).Should(BeEmpty())
	})
})

// reconcilerSeries sums the value of one series over the reconciler pods.
func reconcilerSeries(series string) (float64, error) {
	output, err := kubectl("get", "pods", "-l", processSelector("reconciler"), "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return 0, err
	}
	var sum float64
	for pod := range strings.FieldsSeq(output) {
		metrics, err := kubectl("get", "--raw",
			fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/metrics", namespace, pod, metricsPort))
		if err != nil {
			return 0, err
		}
		for line := range strings.SplitSeq(metrics, "\n") {
			value, found := strings.CutPrefix(line, series+" ")
			if !found {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return 0, err
			}
			sum += v
		}
	}
	return sum, nil
}

// monitorPodManifest is a pod of the monitoring namespace running image in one container
// named pause, admissible under the restricted security policy.
func monitorPodManifest(name, image string) string {
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
`, name, monitorNamespace, image)
}
