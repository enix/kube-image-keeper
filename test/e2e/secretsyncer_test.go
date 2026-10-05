//go:build e2e

package e2e

import (
	"encoding/base64"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// syncerNamespace receives the pull Secrets of the syncer specs; its label is what only their
// resources select
const syncerNamespace = "kuik-e2e-secrets"

// syncerLabel is the namespace label the syncer specs' resources select
const syncerLabel = "kuik.enix.io/e2e-secrets"

const (
	// syncedAlternative injects syncedSource, which exists
	syncedAlternative = "e2e-secrets"
	syncedSource      = "e2e-secrets-creds"
	// unresolvedAlternative injects unresolvedSource, which never exists
	unresolvedAlternative = "e2e-secrets-unresolved"
	unresolvedSource      = "e2e-secrets-absent"
)

// syncedSecret is the pull Secret the syncer writes for syncedAlternative
const syncedSecret = "kuik-inject-imagealternative-" + syncedAlternative

var syncerResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[1]s
spec:
  rewritePolicy: Always
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: e2e-secrets.invalid/pause
    - repository: registry.k8s.io/pause
      auth:
        secretRef:
          name: %[3]s
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[4]s
spec:
  rewritePolicy: Always
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: e2e-secrets-unresolved.invalid/pause
    - repository: registry.k8s.io/pause
      auth:
        secretRef:
          name: %[5]s
`, syncedAlternative, syncerLabel, syncedSource, unresolvedAlternative, unresolvedSource)

var _ = Describe("Pull secret syncing", Ordered, func() {
	BeforeAll(func() {
		By("waiting for the secret syncer Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("secret-syncer").Should(Succeed())

		By("creating the namespace the resources select, with no pod in it")
		_, err := kubectl("create", "ns", syncerNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", syncerNamespace, syncerLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("creating the source Secret in the install namespace")
		// A placeholder credential: the specs check where it is copied, never a pull with it.
		_, err = kubectl("create", "secret", "docker-registry", syncedSource, "-n", namespace,
			"--docker-server=registry.k8s.io", "--docker-username=e2e", "--docker-password=e2e")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageAlternatives")
		Expect(kubectlStdin(syncerResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "imagealternatives", syncedAlternative, unresolvedAlternative, "--ignore-not-found")
		_, _ = kubectl("delete", "ns", syncerNamespace, "--wait=false")
		_, _ = kubectl("delete", "secret", syncedSource, "-n", namespace, "--ignore-not-found")
	})

	It("provisions the pull Secret of an Always ImageAlternative in a namespace it selects, before any pod", func() {
		Eventually(kubectl).WithArguments("get", "secret", syncedSecret, "-n", syncerNamespace,
			"-o", "jsonpath={.type}").Should(Equal("kubernetes.io/dockerconfigjson"))

		encoded, err := kubectl("get", "secret", syncedSecret, "-n", syncerNamespace,
			"-o", `jsonpath={.data.\.dockerconfigjson}`)
		Expect(err).NotTo(HaveOccurred())
		config, err := base64.StdEncoding.DecodeString(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(config)).To(ContainSubstring(`"registry.k8s.io/pause"`))
	})

	It("reports a missing source Secret with a PullSecretInjectionFailed event on the resource", func() {
		// The resource is cluster-scoped: its events go through events.k8s.io, outside its namespace.
		Eventually(kubectl).WithArguments("get", "events.events.k8s.io", "-A",
			"-o", `jsonpath={range .items[?(@.reason=="PullSecretInjectionFailed")]}{.regarding.name}{"\n"}{end}`).
			Should(ContainSubstring(unresolvedAlternative))
	})

	It("deletes the pull Secret with its ImageAlternative", func() {
		Eventually(kubectl).WithArguments("get", "secret", syncedSecret, "-n", syncerNamespace, "-o", "name").
			Should(ContainSubstring(syncedSecret))

		_, err := kubectl("delete", "imagealternative", syncedAlternative)
		Expect(err).NotTo(HaveOccurred())

		Eventually(kubectl).WithArguments("get", "secret", syncedSecret, "-n", syncerNamespace,
			"--ignore-not-found", "-o", "name").Should(BeEmpty())
	})
})
