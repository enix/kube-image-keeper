//go:build e2e

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// statusNamespace holds the pods of the status specs; its label is what only statusAlternative
// selects, so the routing specs never see them and the other way round
const statusNamespace = "kuik-e2e-status"

// statusLabel is the namespace label statusAlternative selects
const statusLabel = "kuik.enix.io/e2e-status"

// statusOrigin is an image whose registry does not resolve, with registry.k8s.io as its
// alternative in statusAlternative
const statusOrigin = "e2e-status.invalid/pause:" + pauseTag

// statusAlternative routes statusOrigin to registry.k8s.io
const statusAlternative = "e2e-status"

// secretAlternative names a Secret that does not exist yet
const (
	secretAlternative = "e2e-status-secret"
	statusSecret      = "e2e-status-creds"
)

var statusResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[1]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: e2e-status.invalid/pause
    - repository: registry.k8s.io/pause
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[3]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "none"
  alternatives:
    - repository: e2e-status-secret.invalid/pause
    - repository: registry.k8s.io/pause
      auth:
        secretRef:
          name: %[4]s
`, statusAlternative, statusLabel, secretAlternative, statusSecret)

var _ = Describe("ImageAlternative status", Ordered, func() {
	// routed is the pod the webhook routed to the fallback, once every replica sees the resource
	var routed string

	BeforeAll(func() {
		By("waiting for the webhook and reconciler Deployments to become available")
		Eventually(deploymentAvailable).WithArguments("webhook").Should(Succeed())
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())

		By("creating the status namespace, selected by the resource")
		_, err := kubectl("create", "ns", statusNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", statusNamespace, statusLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageAlternatives")
		Expect(kubectlStdin(statusResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())

		By("creating pods until one is routed, every replica then seeing the resource")
		attempt := 0
		Eventually(func() (string, error) {
			attempt++
			routed = fmt.Sprintf("routed-%d", attempt)
			if _, err := kubectlStdin(statusPodManifest(routed, statusOrigin), "create", "-f", "-"); err != nil {
				return "", err
			}
			return kubectl("get", "pod", routed, "-n", statusNamespace, "-o", "jsonpath={.spec.containers[0].image}")
		}).Should(Equal(availableImage))
		// Only the routed pod stays: the others would count as fallbacks too.
		for i := 1; i < attempt; i++ {
			_, _ = kubectl("delete", "pod", fmt.Sprintf("routed-%d", i), "-n", statusNamespace, "--wait=false")
		}
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "ns", statusNamespace, "--wait=false")
		_, _ = kubectl("delete", "imagealternatives", statusAlternative, secretAlternative, "--ignore-not-found")
		_, _ = kubectl("delete", "secret", statusSecret, "-n", namespace, "--ignore-not-found")
	})

	It("reports the fallback of a pod the webhook routed, in activeFallbacks and FallbackActive", func() {
		Eventually(kubectl).WithArguments("get", "imagealternative", statusAlternative,
			"-o", "jsonpath={.status.activeFallbacks[0].image} {.status.activeFallbacks[0].rewrittenTo}").
			Should(Equal(statusOrigin + " " + availableImage))
		Eventually(kubectl).WithArguments("get", "imagealternative", statusAlternative,
			"-o", `jsonpath={.status.conditions[?(@.type=="FallbackActive")].status}`).
			Should(Equal("True"))
	})

	It("emits ImageFallback on the pod the webhook routed", func() {
		Eventually(kubectl).WithArguments("get", "events", "-n", statusNamespace,
			"--field-selector", "reason=ImageFallback,involvedObject.name="+routed,
			"-o", "jsonpath={.items[*].reason}").
			Should(ContainSubstring("ImageFallback"))
	})

	It("sets Ready to SecretNotFound, then back to IsReady once the Secret exists in the install namespace", func() {
		ready := `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`
		Eventually(kubectl).WithArguments("get", "imagealternative", secretAlternative, "-o", ready).
			Should(Equal("SecretNotFound"))

		By("creating the Secret in the install namespace")
		// A placeholder credential: the spec only checks that the Secret is found and well formed.
		_, err := kubectl("create", "secret", "docker-registry", statusSecret, "-n", namespace,
			"--docker-server=registry.k8s.io", "--docker-username=e2e", "--docker-password=e2e")
		Expect(err).NotTo(HaveOccurred())

		Eventually(kubectl).WithArguments("get", "imagealternative", secretAlternative, "-o", ready).
			Should(Equal("IsReady"))
	})
})

// statusPodManifest is a pod of the status namespace running image in one container named
// pause, admissible under the restricted security policy.
func statusPodManifest(name, image string) string {
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
`, name, statusNamespace, image)
}
