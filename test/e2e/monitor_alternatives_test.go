//go:build e2e

package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// alternativesNamespace holds the pods of the alternatives monitoring specs; its label is what
// only alternativesMonitor and alternativesOffer select.
const alternativesNamespace = "kuik-e2e-monitor-alternatives"

// alternativesLabel is the namespace label alternativesMonitor and alternativesOffer select.
const alternativesLabel = "kuik.enix.io/e2e-monitor-alternatives"

// alternativesMonitor is the ImageMonitor under test, monitoring alternatives.
const alternativesMonitor = "e2e-monitor-alternatives"

// alternativesOffer is the ImageAlternative offering an alternative for the pause image.
const alternativesOffer = "e2e-monitor-alternatives"

// unreachableAlternative is the alternative alternativesOffer offers for availableImage: a host
// that fails at DNS, so that its check is Unreachable at once.
const unreachableAlternative = "e2e-alternative.invalid/pause:" + pauseTag

var alternativesMonitorManifest = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: %s
spec:
  monitorAlternatives: true
  namespaceSelector:
    matchLabels:
      %s: "true"
`, alternativesMonitor, alternativesLabel)

// alternativesOfferManifest is alternativesOffer, its alternative marked unavailable when
// withdrawn is set. Under OnFailure the pause image answers first, so no pod is rewritten.
func alternativesOfferManifest(withdrawn bool) string {
	return fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %s
spec:
  namespaceSelector:
    matchLabels:
      %s: "true"
  alternatives:
    - repository: registry.k8s.io/pause
    - repository: e2e-alternative.invalid/pause
      unavailable: %t
`, alternativesOffer, alternativesLabel, withdrawn)
}

var _ = Describe("Alternative monitoring", Ordered, func() {
	BeforeAll(func() {
		By("waiting for the reconciler Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())

		By("creating the namespace, selected by the monitor and the ImageAlternative")
		_, err := kubectl("create", "ns", alternativesNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", alternativesNamespace, alternativesLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageAlternative and the ImageMonitor, and creating a pod they select")
		Expect(kubectlStdin(alternativesOfferManifest(false), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Expect(kubectlStdin(alternativesMonitorManifest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Expect(kubectlStdin(pausePodManifest(alternativesNamespace, "monitored", availableImage), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "ns", alternativesNamespace, "--wait=false")
		_, _ = kubectl("delete", "imagemonitor", alternativesMonitor, "--ignore-not-found")
		_, _ = kubectl("delete", "imagealternative", alternativesOffer, "--ignore-not-found")
	})

	It("lists an unreachable alternative and its via in unavailableAlternatives and sets AlternativesUnavailable", func() {
		// The reconciler checks it on its own windows, one minute apart by default: no reconcile
		// is asked for.
		entry := fmt.Sprintf(`jsonpath={range .status.unavailableAlternatives[?(@.ref==%q)]}`+
			`{.reason}/{.via}/{.derivedFrom}{end}`, unreachableAlternative)
		Eventually(kubectl).WithTimeout(4*time.Minute).WithArguments("get", "imagemonitor", alternativesMonitor,
			"-o", entry).Should(Equal("Unreachable/ImageAlternative/" + alternativesOffer + "/" + availableImage))
		Expect(kubectl("get", "imagemonitor", alternativesMonitor,
			"-o", `jsonpath={.status.conditions[?(@.type=="AlternativesUnavailable")].status}`)).To(Equal("True"))
	})

	It("follows an ImageAlternative edit without any pod change", func() {
		Expect(kubectl("get", "imagemonitor", alternativesMonitor,
			"-o", "jsonpath={.status.images.alternatives.tracked}")).To(Equal("1"))

		// The entry is withdrawn: the watch on ImageAlternatives brings a report, no pod moves.
		Expect(kubectlStdin(alternativesOfferManifest(true), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "imagemonitor", alternativesMonitor,
			"-o", "jsonpath={.status.images.alternatives.tracked} {.status.unavailableAlternatives}").
			Should(Equal("0 "))
		Expect(kubectl("get", "imagemonitor", alternativesMonitor,
			"-o", `jsonpath={.status.conditions[?(@.type=="AlternativesUnavailable")].status}`)).To(BeEmpty())
	})
})
