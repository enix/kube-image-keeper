//go:build e2e

package e2e

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/utils"
)

// mirrorNamespace holds the pods the mirrors copy; its label is what only they select
const mirrorNamespace = "kuik-e2e-mirror"

// mirrorLabel is the namespace label the mirrors select
const mirrorLabel = "kuik.enix.io/e2e-mirror"

// registryNamespace holds the destination registry, out of the mirrors' reach: they would copy
// its own images otherwise
const registryNamespace = "kuik-e2e-registry"

// registryHost is the destination registry, named through the cluster DNS: go-containerregistry
// speaks plain HTTP to a private IP on its own, which would make destination.insecure moot
const registryHost = "registry." + registryNamespace + ".svc.cluster.local:5000"

// registryStorage is where the destination registry keeps its repositories
const registryStorage = "/var/lib/registry/docker/registry/v2/repositories"

// pauseManifests are the single-platform manifests of availableImage, by node architecture. A
// mirror copies an index whole, Windows layers included (some 570 MB for pause): pinned to one
// Linux manifest, a copy moves about 300 KB.
var pauseManifests = map[string]string{
	"amd64": "sha256:7c38f24774e3cbd906d2d33c38354ccf787635581c122965132c9bd309754d4a",
	"arm64": "sha256:e50b7059b633caf3c1449b8da680d11845cda4506b513ee7a2de00725f0a34a7",
}

// A placeholder credential of the throwaway destination registry, which only this suite runs.
const (
	registryUser     = "e2e"
	registryPassword = "e2e-mirror"
)

// manageSecret is the write credential of the mirrors, in the install namespace
const manageSecret = "e2e-mirror-manage"

const (
	// copyMirror reaches the destination over plain HTTP from the start
	copyMirror = "e2e-mirror"
	// httpMirror starts without destination.insecure
	httpMirror = "e2e-mirror-http"
)

// registryManifest is a distribution v3 registry accepting tag deletion behind htpasswd, its
// password file written by an init container
var registryManifest = fmt.Sprintf(`
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
      initContainers:
        - name: htpasswd
          image: httpd:2.4-alpine
          command: [sh, -c, "htpasswd -Bbn %[2]s %[3]s > /auth/htpasswd"]
          volumeMounts:
            - name: auth
              mountPath: /auth
      containers:
        - name: registry
          image: registry:3.0.0
          env:
            - name: REGISTRY_STORAGE_DELETE_ENABLED
              value: "true"
            - name: REGISTRY_AUTH
              value: htpasswd
            - name: REGISTRY_AUTH_HTPASSWD_REALM
              value: e2e
            - name: REGISTRY_AUTH_HTPASSWD_PATH
              value: /auth/htpasswd
          ports:
            - containerPort: 5000
          volumeMounts:
            - name: auth
              mountPath: /auth
      volumes:
        - name: auth
          emptyDir: {}
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
`, registryNamespace, registryUser, registryPassword)

// mirrorManifest is an ImageMirror copying the pods of mirrorNamespace to repository under the
// destination registry. It never routes: the kubelet cannot pull from a plain HTTP registry the
// node runtime does not know.
func mirrorManifest(name, repository string, insecure bool) string {
	return fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: %s
spec:
  rewritePolicy: None
  namespaceSelector:
    matchLabels:
      %s: "true"
  destination:
    path: %s/%s/
    insecure: %t
    manage:
      auth:
        secretRef:
          name: %s
`, name, mirrorLabel, registryHost, repository, insecure, manageSecret)
}

var _ = Describe("Image mirroring", Ordered, func() {
	var clusterID, mirroredImage string

	BeforeAll(func() {
		By("waiting for the reconciler Deployment to become available")
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())

		By("reading the cluster identity and opening a copy window every 5s on registry.k8s.io")
		original, err := kubectl("get", "configmap", configMapName, "-n", namespace,
			"-o", `jsonpath={.data.config\.yaml}`)
		Expect(err).NotTo(HaveOccurred())
		for line := range strings.SplitSeq(original, "\n") {
			if value, found := strings.CutPrefix(line, "clusterID: "); found {
				clusterID = strings.Trim(value, `"' `)
			}
		}
		Expect(clusterID).NotTo(BeEmpty())
		original = strings.TrimRight(original, "\n") + "\n"
		DeferCleanup(setConfig, original)
		setConfig(original + "registries:\n  registry.k8s.io:\n    copy:\n      interval: 5s\n")

		By("deploying the destination registry")
		_, err = kubectl("create", "ns", registryNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(kubectlStdin(registryManifest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("rollout", "status", "deployment/registry", "-n", registryNamespace,
			"--timeout=30s").Should(ContainSubstring("successfully rolled out"))

		By("creating the write credential in the install namespace")
		_, err = kubectl("create", "secret", "docker-registry", manageSecret, "-n", namespace,
			"--docker-server="+registryHost, "--docker-username="+registryUser, "--docker-password="+registryPassword)
		Expect(err).NotTo(HaveOccurred())

		By("running a pod in the namespace the mirrors select")
		_, err = kubectl("create", "ns", mirrorNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", mirrorNamespace, mirrorLabel+"=true")
		Expect(err).NotTo(HaveOccurred())
		arch, err := kubectl("get", "nodes", "-o", "jsonpath={.items[0].status.nodeInfo.architecture}")
		Expect(err).NotTo(HaveOccurred())
		Expect(pauseManifests).To(HaveKey(arch), "no pinned pause manifest for this node architecture")
		mirroredImage = availableImage + "@" + pauseManifests[arch]
		Expect(kubectlStdin(mirrorPodManifest("pause", mirroredImage), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "pod", "pause", "-n", mirrorNamespace,
			"-o", "jsonpath={.status.phase}").Should(Equal("Running"))
	})

	AfterAll(func() {
		// A mirror holds its deletion until its tags are gone: give it a minute while the
		// registry still answers, then drop what holds it, or the CRD uninstall of the suite
		// would wait on it. The deletion spec is what checks the finalizer.
		_, _ = kubectl("delete", "imagemirrors", copyMirror, httpMirror, "--ignore-not-found", "--wait=false")
		_, _ = kubectl("wait", "--for=delete", "imagemirror/"+copyMirror, "imagemirror/"+httpMirror, "--timeout=50s")
		for _, name := range []string{copyMirror, httpMirror} {
			_, _ = kubectl("patch", "imagemirror", name, "--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
		}
		_, _ = kubectl("delete", "ns", mirrorNamespace, registryNamespace, "--wait=false")
		_, _ = kubectl("delete", "secret", manageSecret, "-n", namespace, "--ignore-not-found")
	})

	Context("with a plain HTTP destination registry in the cluster", func() {
		It("copies the image of a selected pod to the destination with its manage credential, under the tag of this cluster",
			func() {
				Expect(kubectlStdin(mirrorManifest(copyMirror, "copy", true), "apply", "-f", "-")).
					Error().NotTo(HaveOccurred())

				// The registry refuses an anonymous push: a tag there was written with the credential.
				Eventually(registryTags, "4m").WithArguments("copy/registry.k8s.io/pause").
					Should(ContainElement(pauseTag + "_" + clusterID))
			})

		It("reaches the destination over plain HTTP only when destination.insecure is set", func() {
			Expect(kubectlStdin(mirrorManifest(httpMirror, "http", false), "apply", "-f", "-")).
				Error().NotTo(HaveOccurred())

			By("failing the copy while the mirror speaks HTTPS to a plain HTTP registry")
			Eventually(kubectl, "4m").WithArguments("get", "imagemirror", httpMirror,
				"-o", "jsonpath={.status.failedImageCopies[*].ref}").Should(ContainSubstring(mirroredImage))
			Expect(registryTags("http/registry.k8s.io/pause")).To(BeEmpty())

			By("copying once destination.insecure is set")
			_, err := kubectl("patch", "imagemirror", httpMirror, "--type=merge",
				"-p", `{"spec":{"destination":{"insecure":true}}}`)
			Expect(err).NotTo(HaveOccurred())
			Eventually(registryTags).WithArguments("http/registry.k8s.io/pause").
				Should(ContainElement(pauseTag + "_" + clusterID))
		})

		It("deletes its tags from the destination before the deleted ImageMirror goes away", func() {
			Expect(registryTags("copy/registry.k8s.io/pause")).NotTo(BeEmpty())

			_, err := kubectl("delete", "imagemirror", copyMirror, "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			// httpMirror shares the prefix of copyMirror: compare whole names.
			Eventually(func() ([]string, error) {
				output, err := kubectl("get", "imagemirrors", "-o", "name")
				return utils.GetNonEmptyLines(output), err
			}).ShouldNot(ContainElement("imagemirror.kuik.enix.io/" + copyMirror))
			Expect(registryTags("copy/registry.k8s.io/pause")).To(BeEmpty())
		})
	})
})

// mirrorPodManifest is a pod of the mirror namespace running image in one container named
// pause, admissible under the restricted security policy.
func mirrorPodManifest(name, image string) string {
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
`, name, mirrorNamespace, image)
}

// registryTags lists the tags the destination registry holds in repository, read from its
// storage: an empty list when the repository or its tags are gone.
func registryTags(repository string) ([]string, error) {
	output, err := kubectl("exec", "-n", registryNamespace, "deploy/registry", "-c", "registry", "--",
		"sh", "-c", fmt.Sprintf("ls %s/%s/_manifests/tags 2>/dev/null || true", registryStorage, repository))
	if err != nil {
		return nil, err
	}
	return strings.Fields(output), nil
}
