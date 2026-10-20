//go:build e2e

package e2e

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

// tlsNamespace holds the pod the TLS mirror copies and the pods the TLS reviews admit; its label
// is what only the TLS resources select
const tlsNamespace = "kuik-e2e-tls"

// tlsLabel is the namespace label the TLS resources select
const tlsLabel = "kuik.enix.io/e2e-tls"

// tlsRegistryNamespace holds the HTTPS registry, out of the TLS mirror's reach
const tlsRegistryNamespace = "kuik-e2e-tls-registry"

// tlsRegistryHost is the HTTPS registry under the name its certificate holds, signed by a CA
// of the suite: only its caSecretRef entry lets kuik trust it
const tlsRegistryHost = "registry." + tlsRegistryNamespace + ".svc.cluster.local:5000"

// unverifiedHost is the same registry under a name its certificate does not hold: only an
// insecureSkipVerify entry reaches it
const unverifiedHost = "unverified." + tlsRegistryNamespace + ".svc.cluster.local:5000"

// tlsCASecret holds the CA of the registry, in the install namespace
const tlsCASecret = "e2e-tls-ca"

// tlsMirror copies the pod of tlsNamespace to the HTTPS registry, under mirroredRepository
const tlsMirror = "e2e-tls-mirror"

// mirroredRepository is where tlsMirror writes registry.k8s.io/pause
const mirroredRepository = "mirror/registry.k8s.io/pause"

const (
	// caAlternative offers the registry, through its caSecretRef entry, for caOrigin
	caAlternative = "e2e-tls-ca"
	caOrigin      = "e2e-tls-ca.invalid/pause"
	// skipAlternative offers the registry, through its insecureSkipVerify entry, for skipOrigin
	skipAlternative = "e2e-tls-skip"
	skipOrigin      = "e2e-tls-skip.invalid/pause"
)

// tlsConfig is merged into the global config for the container: a copy window every 5s on
// registry.k8s.io, active checks cached 1s so that a CA change shows at once, and the 2 tls
// entries of the registry
var tlsConfig = fmt.Sprintf(`
registries:
  registry.k8s.io:
    copy:
      interval: 5s
webhook:
  availabilityCheck:
    activeCheckCache:
      ttl: 1s
tls:
  %q:
    caSecretRef:
      name: %s
  %q:
    insecureSkipVerify: true
`, tlsRegistryHost, tlsCASecret, unverifiedHost)

// tlsRegistryManifest is a distribution v3 registry serving HTTPS alone with the certificate of
// the Secret registry-tls, behind 2 Services: registry, the name the certificate holds, and
// unverified, a name it does not hold
var tlsRegistryManifest = fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: registry
  namespace: %[1]s
spec:
  selector:
    matchLabels:
      app: registry
  template:
    metadata:
      labels:
        app: registry
    spec:
      containers:
        - name: registry
          image: registry:3.0.0
          env:
            - name: REGISTRY_STORAGE_DELETE_ENABLED
              value: "true"
            - name: REGISTRY_HTTP_TLS_CERTIFICATE
              value: /certs/tls.crt
            - name: REGISTRY_HTTP_TLS_KEY
              value: /certs/tls.key
          ports:
            - containerPort: 5000
          volumeMounts:
            - name: certs
              mountPath: /certs
              readOnly: true
      volumes:
        - name: certs
          secret:
            secretName: registry-tls
---
apiVersion: v1
kind: Service
metadata:
  name: registry
  namespace: %[1]s
spec:
  selector:
    app: registry
  ports:
    - port: 5000
---
apiVersion: v1
kind: Service
metadata:
  name: unverified
  namespace: %[1]s
spec:
  selector:
    app: registry
  ports:
    - port: 5000
`, tlsRegistryNamespace)

// tlsResources are the mirror filling the registry and the 2 ImageAlternatives offering it
var tlsResources = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: %[1]s
spec:
  rewritePolicy: None
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  destination:
    path: %[3]s/mirror/
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[4]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: %[5]s
    - repository: %[3]s/%[8]s
---
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %[6]s
spec:
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  alternatives:
    - repository: %[7]s
    - repository: %[9]s/%[8]s
`, tlsMirror, tlsLabel, tlsRegistryHost, caAlternative, caOrigin, skipAlternative, skipOrigin,
	mirroredRepository, unverifiedHost)

// admitted is a pod as one webhook replica admitted it: its image and its kuik annotations,
// each decoded from its JSON
type admitted struct {
	Image       string
	Annotations map[string]any
}

var _ = Describe("Registry TLS", Ordered, func() {
	// digest is the single-platform pause manifest the mirror copies. A pod pinned to it finds
	// it on the mirror's repository whatever tag the mirror wrote it under.
	var digest string

	BeforeAll(func() {
		startAPIProxy()

		By("waiting for the webhook and reconciler Deployments to become available")
		Eventually(deploymentAvailable).WithArguments("webhook").Should(Succeed())
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())

		By("issuing the registry certificate from a CA of the suite")
		ca := registrytest.NewCA()
		certificate := ca.Certificate(strings.Split(tlsRegistryHost, ":")[0])
		key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
		Expect(err).NotTo(HaveOccurred())
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})

		By("deploying the HTTPS registry")
		_, err = kubectl("create", "ns", tlsRegistryNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(kubectlStdin(secretManifest(tlsRegistryNamespace, "registry-tls", "kubernetes.io/tls",
			map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM}), "apply", "-f", "-")).
			Error().NotTo(HaveOccurred())
		Expect(kubectlStdin(tlsRegistryManifest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("rollout", "status", "deployment/registry", "-n", tlsRegistryNamespace,
			"--timeout=30s").Should(ContainSubstring("successfully rolled out"))

		By("creating the CA Secret in the install namespace")
		Expect(kubectlStdin(secretManifest(namespace, tlsCASecret, "Opaque",
			map[string][]byte{"ca.crt": ca.PEM()}), "apply", "-f", "-")).Error().NotTo(HaveOccurred())

		By("merging the tls entries into the global config")
		original, err := kubectl("get", "configmap", configMapName, "-n", namespace,
			"-o", `jsonpath={.data.config\.yaml}`)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(setConfig, strings.TrimRight(original, "\n")+"\n")
		setConfig(mergeConfig(original, tlsConfig))

		By("running a pinned pause pod in the namespace the TLS resources select")
		_, err = kubectl("create", "ns", tlsNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", tlsNamespace, tlsLabel+"=true")
		Expect(err).NotTo(HaveOccurred())
		arch, err := kubectl("get", "nodes", "-o", "jsonpath={.items[0].status.nodeInfo.architecture}")
		Expect(err).NotTo(HaveOccurred())
		Expect(pauseManifests).To(HaveKey(arch), "no pinned pause manifest for this node architecture")
		digest = pauseManifests[arch]
		Expect(kubectlStdin(tlsPodManifest("pause", availableImage+"@"+digest), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "pod", "pause", "-n", tlsNamespace,
			"-o", "jsonpath={.status.phase}").Should(Equal("Running"))

		By("applying the mirror and the ImageAlternatives")
		Expect(kubectlStdin(tlsResources, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
	})

	AfterAll(func() {
		// The mirror holds its deletion until its tags are gone: give it a minute while the
		// registry still answers, then drop what holds it.
		_, _ = kubectl("delete", "imagemirror", tlsMirror, "--ignore-not-found", "--wait=false")
		_, _ = kubectl("wait", "--for=delete", "imagemirror/"+tlsMirror, "--timeout=50s")
		_, _ = kubectl("patch", "imagemirror", tlsMirror, "--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
		_, _ = kubectl("delete", "imagealternatives", caAlternative, skipAlternative, "--ignore-not-found")
		_, _ = kubectl("delete", "ns", tlsNamespace, tlsRegistryNamespace, "--wait=false")
		_, _ = kubectl("delete", "secret", tlsCASecret, "-n", namespace, "--ignore-not-found")
	})

	Context("with a caSecretRef entry for an in-cluster registry under a private CA", func() {
		It("copies an ImageMirror's image to that registry", func() {
			// The kubelet syncs the ConfigMap within 3m, then a copy window opens every 5s.
			Eventually(tlsRegistryHolds, "4m").WithArguments(mirroredRepository, digest).Should(BeTrue())
			Expect(kubectl("get", "imagemirror", tlsMirror, "-o", "jsonpath={.status.failedImageCopies}")).
				To(BeEmpty())
		})

		It("rewrites a pod to a candidate on that registry", func() {
			origin := caOrigin + "@" + digest
			candidate := tlsRegistryHost + "/" + mirroredRepository + "@" + digest

			// Each webhook replica syncs the ConfigMap on its own kubelet's schedule.
			Eventually(admittedByEachReplica).WithTimeout(3 * time.Minute).WithArguments(origin).
				Should(everyReplica(Equal(rewritten(caAlternative, origin, candidate))))
		})

		It("applies a change of the CA Secret without restarting the webhook", func() {
			origin := caOrigin + "@" + digest
			candidate := tlsRegistryHost + "/" + mirroredRepository + "@" + digest
			before := webhookRestarts()
			ca, err := kubectl("get", "secret", tlsCASecret, "-n", namespace, "-o", `jsonpath={.data.ca\.crt}`)
			Expect(err).NotTo(HaveOccurred())

			By("leaving the pod untouched once the Secret holds another CA")
			_, err = kubectl("patch", "secret", tlsCASecret, "-n", namespace, "--type=merge",
				"-p", fmt.Sprintf(`{"stringData":{"ca.crt":%q}}`, registrytest.NewCA().PEM()))
			Expect(err).NotTo(HaveOccurred())
			Eventually(admittedByEachReplica).WithArguments(origin).
				Should(everyReplica(Equal(exhausted(caAlternative, origin))))

			By("rewriting it again once the Secret holds the registry's CA")
			_, err = kubectl("patch", "secret", tlsCASecret, "-n", namespace, "--type=merge",
				"-p", fmt.Sprintf(`{"data":{"ca.crt":%q}}`, ca))
			Expect(err).NotTo(HaveOccurred())
			Eventually(admittedByEachReplica).WithArguments(origin).
				Should(everyReplica(Equal(rewritten(caAlternative, origin, candidate))))

			Expect(webhookRestarts()).To(Equal(before))
		})
	})

	Context("with an insecureSkipVerify entry for a name its certificate does not hold", func() {
		It("rewrites a pod to a candidate on that registry", func() {
			origin := skipOrigin + "@" + digest
			candidate := unverifiedHost + "/" + mirroredRepository + "@" + digest

			Eventually(admittedByEachReplica).WithTimeout(30 * time.Second).WithArguments(origin).
				Should(everyReplica(Equal(rewritten(skipAlternative, origin, candidate))))
		})
	})
})

// rewritten is a pod of the container pause rewritten from origin to candidate by the
// ImageAlternative alternative, under its default policy.
func rewritten(alternative, origin, candidate string) admitted {
	return admitted{Image: candidate, Annotations: map[string]any{
		"kuik.enix.io/rewrites": map[string]any{"pause": map[string]any{
			"by": "ImageAlternative/" + alternative, "origin": origin, "rewrittenTo": candidate, "policy": "OnFailure",
		}},
	}}
}

// exhausted is a pod of the container pause left on origin, every candidate of the
// ImageAlternative alternative having failed.
func exhausted(alternative, origin string) admitted {
	return admitted{Image: origin, Annotations: map[string]any{
		"kuik.enix.io/no-alternatives": map[string]any{"pause": []any{"ImageAlternative/" + alternative}},
	}}
}

// admittedByEachReplica is, for each webhook pod, the pod of image it admits in tlsNamespace:
// the AdmissionReview goes to each pod through the API server's proxy, and no pod is created.
func admittedByEachReplica(image string) (map[string]admitted, error) {
	responses, err := replicaResponses(tlsNamespace, tlsPodManifest("tls-probe", image))
	if err != nil {
		return nil, err
	}
	pods := map[string]admitted{}
	for pod, response := range responses {
		var patch []struct {
			Op    string          `json:"op"`
			Path  string          `json:"path"`
			Value json.RawMessage `json:"value"`
		}
		if len(response.Patch) > 0 {
			if err := json.Unmarshal(response.Patch, &patch); err != nil {
				return nil, fmt.Errorf("decoding the patch of %s: %w", pod, err)
			}
		}
		raw := map[string]string{}
		a := admitted{Image: image, Annotations: map[string]any{}}
		for _, op := range patch {
			switch {
			case op.Path == "/spec/containers/0/image":
				if err := json.Unmarshal(op.Value, &a.Image); err != nil {
					return nil, err
				}
			case op.Path == "/metadata/annotations":
				if err := json.Unmarshal(op.Value, &raw); err != nil {
					return nil, err
				}
			case strings.HasPrefix(op.Path, "/metadata/annotations/"):
				key := strings.NewReplacer("~1", "/", "~0", "~").Replace(strings.TrimPrefix(op.Path, "/metadata/annotations/"))
				var value string
				if err := json.Unmarshal(op.Value, &value); err != nil {
					return nil, err
				}
				raw[key] = value
			}
		}
		for key, value := range raw {
			if !strings.HasPrefix(key, kuikAnnotationPrefix) {
				continue
			}
			var decoded any
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return nil, fmt.Errorf("decoding the annotation %s of %s: %w", key, pod, err)
			}
			a.Annotations[key] = decoded
		}
		pods[pod] = a
	}
	return pods, nil
}

// tlsRegistryHolds reports whether the HTTPS registry holds the manifest digest in repository,
// read from its storage.
func tlsRegistryHolds(repository, digest string) (bool, error) {
	hex := strings.TrimPrefix(digest, "sha256:")
	output, err := kubectl("exec", "-n", tlsRegistryNamespace, "deploy/registry", "-c", "registry", "--",
		"sh", "-c", fmt.Sprintf("test -d %s/%s/_manifests/revisions/sha256/%s && echo found || true",
			registryStorage, repository, hex))
	return strings.TrimSpace(output) == "found", err
}

// secretManifest is a Secret of secretType holding data.
func secretManifest(secretNamespace, name, secretType string, data map[string][]byte) string {
	stringData := map[string]string{}
	for key, value := range data {
		stringData[key] = string(value)
	}
	content, _ := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]string{"name": name, "namespace": secretNamespace},
		"type":       secretType,
		"stringData": stringData,
	})
	return string(content)
}

// tlsPodManifest is a pod of tlsNamespace running image in one container named pause,
// admissible under the restricted security policy.
func tlsPodManifest(name, image string) string {
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
`, name, tlsNamespace, image)
}
