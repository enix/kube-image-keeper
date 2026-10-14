//go:build e2e

package e2e

import (
	"encoding/base64"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// writesNamespace is the application namespace of the Secret writes specs; its label is what
// only writesAlternative selects
const writesNamespace = "kuik-e2e-secret-writes"

// writesLabel is the namespace label writesAlternative selects
const writesLabel = "kuik.enix.io/e2e-secret-writes"

const (
	// writesAlternative injects writesSource, so that the syncer writes writesSecret, owned by
	// it, in writesNamespace
	writesAlternative = "e2e-secret-writes"
	writesSource      = "e2e-secret-writes-creds"
	writesSecret      = "kuik-inject-imagealternative-" + writesAlternative
)

// secretWritesPolicy is the name of the ValidatingAdmissionPolicy the chart installs, which the
// API server names in each refusal
const secretWritesPolicy = "kube-image-keeper-secret-syncer"

// The messages of the validations of secretWritesPolicy, as the API server reports them.
const (
	refusedType   = "may only write Secrets of type kubernetes.io/dockerconfigjson"
	refusedLabel  = "may only write Secrets labelled app.kubernetes.io/managed-by=kuik-secret-syncer"
	refusedName   = "may only write Secrets named kuik-inject-*"
	refusedOthers = "are reserved to the kuik secret syncer"
)

// The .dockerconfigjson of the Secrets the specs write: emptyConfig, and renewedConfig, a
// placeholder credential no pull ever uses.
var (
	emptyConfig   = base64.StdEncoding.EncodeToString([]byte(`{"auths":{}}`))
	renewedConfig = base64.StdEncoding.EncodeToString([]byte(`{"auths":{"registry.k8s.io":{"auth":"ZTJlOmUyZQ=="}}}`))
)

var writesResource = fmt.Sprintf(`
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
    - repository: e2e-secret-writes.invalid/pause
    - repository: registry.k8s.io/pause
      auth:
        secretRef:
          name: %[3]s
`, writesAlternative, writesLabel, writesSource)

// asSyncer impersonates the ServiceAccount of the secret syncer. Its RBAC grants create and
// patch on Secrets, never get: the specs write as the syncer with create and server-side apply
// only.
var asSyncer = "--as=system:serviceaccount:" + namespace + ":" + serviceAccountName("secret-syncer")

// pullSecretManifest is a Secret named name in ns, of type secretType, carrying the managed-by
// label of the syncer when managed, and config as its .dockerconfigjson.
func pullSecretManifest(ns, name, secretType string, managed bool, config string) string {
	labels := ""
	if managed {
		labels = "\n  labels:\n    app.kubernetes.io/managed-by: kuik-secret-syncer"
	}
	return fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s%s
type: %s
data:
  .dockerconfigjson: %s
`, name, ns, labels, secretType, config)
}

// syncerCreate creates manifest as the secret syncer.
func syncerCreate(manifest string) (string, error) {
	return kubectlStdin(manifest, "create", "-f", "-", asSyncer)
}

// syncerApply applies manifest as the secret syncer does: a forced server-side apply under its
// field manager.
func syncerApply(manifest string) (string, error) {
	return kubectlStdin(manifest, "apply", "--server-side", "--field-manager=kuik-secret-syncer",
		"--force-conflicts", "-f", "-", asSyncer)
}

// expectPolicyRefusal asserts that a write failed on secretWritesPolicy with message, rather
// than on RBAC or anything else.
func expectPolicyRefusal(output string, err error, message string) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(output).To(ContainSubstring("ValidatingAdmissionPolicy '" + secretWritesPolicy + "'"))
	Expect(output).To(ContainSubstring(message))
}

var _ = Describe("Secret writes admission", Ordered, func() {
	BeforeAll(func() {
		By("waiting for the secret syncer Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("secret-syncer").Should(Succeed())

		By("creating the application namespace the ImageAlternative selects")
		_, err := kubectl("create", "ns", writesNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", writesNamespace, writesLabel+"=true")
		Expect(err).NotTo(HaveOccurred())

		By("creating the source Secret in the install namespace")
		// A placeholder credential: the specs check who may write the copy, never a pull with it.
		_, err = kubectl("create", "secret", "docker-registry", writesSource, "-n", namespace,
			"--docker-server=registry.k8s.io", "--docker-username=e2e", "--docker-password=e2e")
		Expect(err).NotTo(HaveOccurred())

		By("applying the ImageAlternative, so that the syncer writes a pull Secret it owns")
		Expect(kubectlStdin(writesResource, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "secret", writesSecret, "-n", writesNamespace,
			"-o", "jsonpath={.metadata.ownerReferences[0].name}").Should(Equal(writesAlternative))
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "imagealternative", writesAlternative, "--ignore-not-found")
		_, _ = kubectl("delete", "ns", writesNamespace, "--wait=false")
		_, _ = kubectl("delete", "secret", writesSource, "-n", namespace, "--ignore-not-found")
	})

	Context("by the secret syncer", func() {
		It("admits a pull Secret it owns under kuik-inject- in an application namespace", func() {
			Expect(syncerCreate(pullSecretManifest(writesNamespace, "kuik-inject-e2e-admitted",
				"kubernetes.io/dockerconfigjson", true, emptyConfig))).Error().NotTo(HaveOccurred())
		})

		It("admits a pull Secret it owns under kuik-provider- in the install namespace", func() {
			const name = "kuik-provider-e2e-admitted"
			DeferCleanup(kubectl, "delete", "secret", name, "-n", namespace, "--ignore-not-found")
			Expect(syncerCreate(pullSecretManifest(namespace, name,
				"kubernetes.io/dockerconfigjson", true, emptyConfig))).Error().NotTo(HaveOccurred())
		})

		It("admits the renewal of a pull Secret it already wrote", func() {
			const name = "kuik-inject-e2e-renewed"
			Expect(syncerCreate(pullSecretManifest(writesNamespace, name,
				"kubernetes.io/dockerconfigjson", true, emptyConfig))).Error().NotTo(HaveOccurred())

			Expect(syncerApply(pullSecretManifest(writesNamespace, name,
				"kubernetes.io/dockerconfigjson", true, renewedConfig))).Error().NotTo(HaveOccurred())
			Expect(kubectl("get", "secret", name, "-n", writesNamespace,
				"-o", `jsonpath={.data.\.dockerconfigjson}`)).To(Equal(renewedConfig))
		})

		DescribeTable("refuses a Secret that is not a pull Secret it owns",
			func(write func() (string, error), message string) {
				output, err := write()
				expectPolicyRefusal(output, err, message)
			},
			Entry("of a type other than kubernetes.io/dockerconfigjson", func() (string, error) {
				return syncerCreate(pullSecretManifest(writesNamespace, "kuik-inject-e2e-opaque",
					"Opaque", true, emptyConfig))
			}, refusedType),
			Entry("losing the managed-by label on an update", func() (string, error) {
				const name = "kuik-inject-e2e-unlabelled"
				// Both writes go through the syncer's field manager, so that the second one,
				// which omits the label, removes it.
				Expect(syncerApply(pullSecretManifest(writesNamespace, name,
					"kubernetes.io/dockerconfigjson", true, emptyConfig))).Error().NotTo(HaveOccurred())
				return syncerApply(pullSecretManifest(writesNamespace, name,
					"kubernetes.io/dockerconfigjson", false, emptyConfig))
			}, refusedLabel),
			Entry("named outside the reserved prefixes", func() (string, error) {
				return syncerCreate(pullSecretManifest(writesNamespace, "e2e-not-reserved",
					"kubernetes.io/dockerconfigjson", true, emptyConfig))
			}, refusedName),
			Entry("under kuik-provider- outside the install namespace", func() (string, error) {
				return syncerCreate(pullSecretManifest(writesNamespace, "kuik-provider-e2e-elsewhere",
					"kubernetes.io/dockerconfigjson", true, emptyConfig))
			}, refusedName),
		)
	})

	Context("by any other identity", func() {
		DescribeTable("refuses to create a Secret under a reserved prefix, in an application namespace",
			func(prefix string) {
				// A pull Secret the syncer itself would be admitted to write: only the identity
				// differs.
				output, err := kubectlStdin(pullSecretManifest(writesNamespace, prefix+"e2e-squatted",
					"kubernetes.io/dockerconfigjson", true, emptyConfig), "create", "-f", "-")
				expectPolicyRefusal(output, err, refusedOthers)
			},
			Entry("kuik-inject-", "kuik-inject-"),
			Entry("kuik-provider-", "kuik-provider-"),
		)

		It("refuses any change to the content of a pull Secret the syncer wrote", func() {
			output, err := kubectl("patch", "secret", writesSecret, "-n", writesNamespace, "--type=merge",
				"-p", fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`, emptyConfig))
			expectPolicyRefusal(output, err, refusedOthers)
		})

		It("refuses marking a pull Secret the syncer wrote immutable", func() {
			output, err := kubectl("patch", "secret", writesSecret, "-n", writesNamespace, "--type=merge",
				"-p", `{"immutable":true}`)
			expectPolicyRefusal(output, err, refusedOthers)
		})

		It("refuses removing the owner reference of a pull Secret the syncer wrote", func() {
			output, err := kubectl("patch", "secret", writesSecret, "-n", writesNamespace, "--type=json",
				"-p", `[{"op":"remove","path":"/metadata/ownerReferences"}]`)
			expectPolicyRefusal(output, err, refusedOthers)
		})

		It("admits a rewrite that changes nothing of a pull Secret the syncer wrote", func() {
			// What rotating the encryption at rest does to every Secret of the cluster.
			current, err := kubectl("get", "secret", writesSecret, "-n", writesNamespace, "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			Expect(kubectlStdin(current, "replace", "-f", "-")).Error().NotTo(HaveOccurred())
		})

		It("admits a Secret named outside the reserved prefixes", func() {
			// Neither a pull Secret nor labelled: the policy leaves every other Secret alone.
			Expect(kubectlStdin(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  generateName: e2e-unreserved-
  namespace: %s
stringData:
  key: value
`, writesNamespace), "create", "-f", "-")).Error().NotTo(HaveOccurred())
		})
	})

	Context("by the garbage collector", func() {
		It("orphans a pull Secret when its ImageAlternative is deleted with orphan propagation", func() {
			_, err := kubectl("delete", "imagealternative", writesAlternative, "--cascade=orphan", "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			// The garbage collector drops the owner reference, then the orphan finalizer: a
			// refused update would leave the ImageAlternative stuck with it.
			Eventually(kubectl).WithArguments("get", "imagealternative", writesAlternative,
				"--ignore-not-found", "-o", "name").Should(BeEmpty())
			Eventually(kubectl).WithArguments("get", "secret", writesSecret, "-n", writesNamespace,
				"-o", "jsonpath={.metadata.name}/{.metadata.ownerReferences}").Should(Equal(writesSecret + "/"))
		})
	})
})
