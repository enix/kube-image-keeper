//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

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

// destinationRegistry is a distribution v3 registry of registryNamespace, behind htpasswd, that
// the mirrors write to with the manage credential secret
type destinationRegistry struct {
	// name is the name of its Deployment and its Service
	name string
	// refuseDeletes puts a proxy in front of the registry that answers 400 UNSUPPORTED to every
	// DELETE but a manifest's by digest: distribution deletes a tag even with storage deletion
	// disabled
	refuseDeletes bool
	// secret is its write credential, in the install namespace
	secret string
}

var (
	// deletingRegistry accepts tag deletion
	deletingRegistry = destinationRegistry{name: "registry", secret: "e2e-mirror-manage"}
	// refusingRegistry refuses tag deletion as unsupported and accepts manifest deletion by
	// digest, as Harbor does
	refusingRegistry = destinationRegistry{
		name: "registry-nodelete", refuseDeletes: true, secret: "e2e-mirror-manage-nodelete",
	}
	// sourceRegistry is the plain HTTP origin of the httpOrigin pods, seeded by seedMirror; its
	// proxy logs when each request came, which shows the requestInterval of its host
	sourceRegistry = destinationRegistry{name: "registry-source", refuseDeletes: true, secret: "e2e-mirror-manage-source"}
)

// host names the registry through the cluster DNS: go-containerregistry speaks plain HTTP to a
// private IP on its own, which would make destination.insecure moot
func (r destinationRegistry) host() string {
	return r.name + "." + registryNamespace + ".svc.cluster.local:5000"
}

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

const (
	// copyMirror reaches the destination over plain HTTP from the start
	copyMirror = "e2e-mirror"
	// httpMirror starts without destination.insecure
	httpMirror = "e2e-mirror-http"
	// refusedMirror copies to refusingRegistry
	refusedMirror = "e2e-mirror-refused"
	// digestMirror copies to refusingRegistry and deletes by digest
	digestMirror = "e2e-mirror-digest"
	// routedMirror routes the pods of mirrorNamespace to its copies on deletingRegistry
	routedMirror = "e2e-mirror-routed"
	// downMirror routes the pods of mirrorNamespace labelled downPodLabel to a destination that
	// does not resolve
	downMirror = "e2e-mirror-down"
	// heldMirror routes the pods of mirrorNamespace to its copies on deletingRegistry, with
	// cleanup, and is deleted while a pod runs one of them
	heldMirror = "e2e-mirror-held"
	// seedMirror copies the pause image to sourceRegistry, then goes, leaving its tag as the
	// image of the httpOrigin pods
	seedMirror = "e2e-mirror-seed"
	// pacedMirror copies the image of the httpOrigin pods from sourceRegistry to deletingRegistry
	pacedMirror = "e2e-mirror-paced"
)

const (
	// httpOriginNamespace holds the pods whose origin is sourceRegistry; its label is what only
	// httpOriginAlternative, httpOriginMonitor and pacedMirror select
	httpOriginNamespace = "kuik-e2e-http-origin"
	// httpOriginLabel is the namespace label of httpOriginNamespace
	httpOriginLabel = "kuik.enix.io/e2e-http-origin"
	// httpOriginAlternative declares sourceRegistry insecure, or not, for the pods of
	// httpOriginNamespace
	httpOriginAlternative = "e2e-http-origin"
	// httpOriginMonitor monitors the pods of httpOriginNamespace
	httpOriginMonitor = "e2e-http-origin"
	// httpOriginSecret is the pull Secret of the pods of httpOriginNamespace, the read credential
	// of sourceRegistry
	httpOriginSecret = "e2e-http-origin"
	// sourceRequestInterval is the requestInterval test/e2e/values.yaml sets on the host of
	// sourceRegistry
	sourceRequestInterval = time.Second
)

// downPodLabel is the pod label downMirror selects: the running pause pod lacks it
const downPodLabel = "kuik.enix.io/e2e-mirror-down"

// refusingProxyConfig is the nginx in front of a registry that refuses tag deletion: every
// DELETE but a manifest's by digest gets 400 UNSUPPORTED, as Harbor answers a tag deletion
// (goharbor/harbor#24213), and any other request reaches the registry unchanged, Host and port
// included, so that the upload locations and the Basic challenge it returns name the proxy.
// Bodies stream both ways, unbounded. Each request is logged as proxyLogLine reads it.
const refusingProxyConfig = `
events {}
http {
  log_format timed '$msec $request_time $remote_addr $request_method $uri $status';
  access_log /dev/stdout timed;
  map "$request_method:$uri" $refused {
    default 0;
    "~^DELETE:/v2/.+/manifests/sha256:" 0;
    "~^DELETE:" 1;
  }
  server {
    listen 5000;
    client_max_body_size 0;
    proxy_request_buffering off;
    proxy_buffering off;
    default_type application/json;
    location / {
      if ($refused) {
        return 400 '{"errors":[{"code":"UNSUPPORTED","message":"the e2e proxy refuses deletion"}]}';
      }
      proxy_pass http://127.0.0.1:5002;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_read_timeout 900s;
      proxy_send_timeout 900s;
    }
  }
}
`

// refusingProxyImage is the nginx of refusingProxyConfig, pulled from Docker Hub like the registry
const refusingProxyImage = "nginx:1.27.4-alpine"

// manifest is the Deployment and the Service of the registry, its password file written by an
// init container. A registry that refuses deletes listens on loopback behind an nginx serving
// refusingProxyConfig on the port of the Service.
func (r destinationRegistry) manifest() string {
	var registryAddress, proxyContainer, proxyVolume, proxyConfigMap string
	registryPort := 5000
	if r.refuseDeletes {
		// Not 5001: the debug server of the registry image listens there.
		registryPort = 5002
		registryAddress = `
            - name: REGISTRY_HTTP_ADDR
              value: 127.0.0.1:5002`
		proxyContainer = fmt.Sprintf(`
        - name: proxy
          image: %s
          ports:
            - containerPort: 5000
          volumeMounts:
            - name: proxy
              mountPath: /etc/nginx/nginx.conf
              subPath: nginx.conf`, refusingProxyImage)
		proxyVolume = fmt.Sprintf(`
        - name: proxy
          configMap:
            name: %s-proxy`, r.name)
		config, _ := json.Marshal(refusingProxyConfig)
		proxyConfigMap = fmt.Sprintf(`
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-proxy
  namespace: %s
data:
  nginx.conf: %s`, r.name, registryNamespace, config)
	}
	return fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[4]s
  namespace: %[1]s
spec:
  selector:
    matchLabels:
      app: %[4]s
  template:
    metadata:
      labels:
        app: %[4]s
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
              value: /auth/htpasswd%[5]s
          ports:
            - containerPort: %[6]d
          volumeMounts:
            - name: auth
              mountPath: /auth%[7]s
      volumes:
        - name: auth
          emptyDir: {}%[8]s%[9]s
---
apiVersion: v1
kind: Service
metadata:
  name: %[4]s
  namespace: %[1]s
spec:
  selector:
    app: %[4]s
  ports:
    - port: 5000
`, registryNamespace, registryUser, registryPassword, r.name, registryAddress, registryPort,
		proxyContainer, proxyVolume, proxyConfigMap)
}

// mirrorManifest is an ImageMirror copying the pods of mirrorNamespace to repository under
// destination, with its manage credential. It never routes: the kubelet cannot pull from a plain
// HTTP registry the node runtime does not know.
func mirrorManifest(name string, destination destinationRegistry, repository string, insecure bool) string {
	return selectingMirrorManifest(name, mirrorLabel, destination, repository, insecure)
}

// selectingMirrorManifest is mirrorManifest copying the pods of the namespaces labelled
// `<namespaceLabel>: "true"`.
func selectingMirrorManifest(name, namespaceLabel string, destination destinationRegistry, repository string,
	insecure bool) string {
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
`, name, namespaceLabel, destination.host(), repository, insecure, destination.secret)
}

// alwaysMirrorManifest is an ImageMirror routing the pods of mirrorNamespace to its copies under
// path, over plain HTTP, reading the destination with secret for the pushes and the webhook's
// checks alike. It injects no pull Secret, since no node can pull from the destination anyway,
// and deletes its tags once deleted only under cleanup: the registry goes with its namespace. A
// podLabel restricts it to the pods carrying `<podLabel>: "true"`; empty, it applies to every pod
// of the namespace.
func alwaysMirrorManifest(name, path, secret, podLabel string, cleanup bool) string {
	return fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMirror
metadata:
  name: %[1]s
spec:
  rewritePolicy: Always
  namespaceSelector:
    matchLabels:
      %[2]s: "true"
  podSelector:
    matchLabels: {%[5]s}
  cleanup:
    enabled: %[6]t
  destination:
    path: %[3]s
    insecure: true
    manage:
      auth:
        secretRef:
          name: %[4]s
    pull:
      auth:
        secretRef:
          name: %[4]s
        injectPullSecret: false
`, name, mirrorLabel, path, secret, trueLabel(podLabel), cleanup)
}

var _ = Describe("Image mirroring", Ordered, func() {
	var clusterID, pauseDigest, mirroredImage string

	BeforeAll(func() {
		startAPIProxy()

		By("waiting for the reconciler and webhook Deployments to become available")
		Eventually(deploymentAvailable).WithArguments("reconciler").Should(Succeed())
		Eventually(deploymentAvailable).WithArguments("webhook").Should(Succeed())

		By("reading the cluster ID the reconciler generated")
		var err error
		clusterID, err = kubectl("get", "configmap", "kuik-cluster-id", "-n", namespace,
			"-o", "jsonpath={.data.clusterID}")
		Expect(err).NotTo(HaveOccurred())
		clusterID = strings.TrimSpace(clusterID)
		Expect(clusterID).NotTo(BeEmpty())

		By("opening a copy window every 5s on registry.k8s.io")
		original, err := kubectl("get", "configmap", configMapName, "-n", namespace,
			"-o", `jsonpath={.data.config\.yaml}`)
		Expect(err).NotTo(HaveOccurred())
		original = strings.TrimRight(original, "\n") + "\n"
		DeferCleanup(setConfig, original)
		setConfig(mergeConfig(original, "registries:\n  registry.k8s.io:\n    copy:\n      interval: 5s\n"))

		By("deploying the destination registries, one refusing tag deletion")
		_, err = kubectl("create", "ns", registryNamespace)
		Expect(err).NotTo(HaveOccurred())
		for _, destination := range []destinationRegistry{deletingRegistry, refusingRegistry} {
			Expect(kubectlStdin(destination.manifest(), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
		}
		for _, destination := range []destinationRegistry{deletingRegistry, refusingRegistry} {
			Eventually(kubectl).WithArguments("rollout", "status", "deployment/"+destination.name, "-n", registryNamespace,
				"--timeout=30s").Should(ContainSubstring("successfully rolled out"))
		}

		By("creating the write credentials in the install namespace")
		for _, destination := range []destinationRegistry{deletingRegistry, refusingRegistry} {
			_, err = kubectl("create", "secret", "docker-registry", destination.secret, "-n", namespace,
				"--docker-server="+destination.host(), "--docker-username="+registryUser, "--docker-password="+registryPassword)
			Expect(err).NotTo(HaveOccurred())
		}

		By("running a pod in the namespace the mirrors select")
		_, err = kubectl("create", "ns", mirrorNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "ns", mirrorNamespace, mirrorLabel+"=true")
		Expect(err).NotTo(HaveOccurred())
		arch, err := kubectl("get", "nodes", "-o", "jsonpath={.items[0].status.nodeInfo.architecture}")
		Expect(err).NotTo(HaveOccurred())
		Expect(pauseManifests).To(HaveKey(arch), "no pinned pause manifest for this node architecture")
		pauseDigest = pauseManifests[arch]
		mirroredImage = availableImage + "@" + pauseDigest
		Expect(kubectlStdin(mirrorPodManifest("pause", mirroredImage), "create", "-f", "-")).
			Error().NotTo(HaveOccurred())
		Eventually(kubectl).WithArguments("get", "pod", "pause", "-n", mirrorNamespace,
			"-o", "jsonpath={.status.phase}").Should(Equal("Running"))
	})

	AfterAll(func() {
		// A mirror holds its deletion until its tags are gone: give it a minute while the
		// registry still answers, then drop what holds it, or the CRD uninstall of the suite
		// would wait on it. The deletion spec is what checks the finalizer.
		// refusedMirror is gone unless its spec failed, and its destination never lets it go.
		// The routing specs and the seeding of the plain HTTP origin delete their mirrors
		// themselves, unless they failed.
		mirrors := []string{copyMirror, httpMirror, refusedMirror, digestMirror, routedMirror, downMirror, heldMirror,
			seedMirror, pacedMirror}
		deletion := append([]string{"delete", "imagemirrors"}, mirrors...)
		_, _ = kubectl(append(deletion, "--ignore-not-found", "--wait=false")...)
		_, _ = kubectl("wait", "--for=delete", "imagemirror/"+copyMirror, "imagemirror/"+httpMirror,
			"imagemirror/"+pacedMirror, "--timeout=50s")
		for _, name := range mirrors {
			_, _ = kubectl("patch", "imagemirror", name, "--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
		}
		_, _ = kubectl("delete", "imagemonitor", httpOriginMonitor, "--ignore-not-found")
		_, _ = kubectl("delete", "imagealternative", httpOriginAlternative, "--ignore-not-found")
		_, _ = kubectl("delete", "ns", mirrorNamespace, httpOriginNamespace, registryNamespace, "--wait=false")
		_, _ = kubectl("delete", "secret", deletingRegistry.secret, refusingRegistry.secret, sourceRegistry.secret,
			"-n", namespace, "--ignore-not-found")
	})

	Context("with a plain HTTP destination registry in the cluster", func() {
		It("copies the image of a selected pod to the destination with its manage credential, under the tag of this cluster",
			func() {
				Expect(kubectlStdin(mirrorManifest(copyMirror, deletingRegistry, "copy", true), "apply", "-f", "-")).
					Error().NotTo(HaveOccurred())

				// The registry refuses an anonymous push: a tag there was written with the credential.
				Eventually(deletingRegistry.tags, "4m").WithArguments("copy/registry.k8s.io/pause").
					Should(ContainElement(pauseTag + "_" + clusterID))
			})

		It("reaches the destination over plain HTTP only when destination.insecure is set", func() {
			Expect(kubectlStdin(mirrorManifest(httpMirror, deletingRegistry, "http", false), "apply", "-f", "-")).
				Error().NotTo(HaveOccurred())

			By("failing the copy while the mirror speaks HTTPS to a plain HTTP registry")
			Eventually(kubectl, "4m").WithArguments("get", "imagemirror", httpMirror,
				"-o", "jsonpath={.status.failedImageCopies[*].ref}").Should(ContainSubstring(mirroredImage))
			Expect(deletingRegistry.tags("http/registry.k8s.io/pause")).To(BeEmpty())

			By("copying once destination.insecure is set")
			_, err := kubectl("patch", "imagemirror", httpMirror, "--type=merge",
				"-p", `{"spec":{"destination":{"insecure":true}}}`)
			Expect(err).NotTo(HaveOccurred())
			Eventually(deletingRegistry.tags).WithArguments("http/registry.k8s.io/pause").
				Should(ContainElement(pauseTag + "_" + clusterID))
		})

		It("deletes its tags from the destination before the deleted ImageMirror goes away", func() {
			Expect(deletingRegistry.tags("copy/registry.k8s.io/pause")).NotTo(BeEmpty())

			_, err := kubectl("delete", "imagemirror", copyMirror, "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			// httpMirror shares the prefix of copyMirror: compare whole names.
			Eventually(func() ([]string, error) {
				output, err := kubectl("get", "imagemirrors", "-o", "name")
				return utils.GetNonEmptyLines(output), err
			}).ShouldNot(ContainElement("imagemirror.kuik.enix.io/" + copyMirror))
			Expect(deletingRegistry.tags("copy/registry.k8s.io/pause")).To(BeEmpty())
		})
	})

	Context("with a destination registry refusing tag deletion", func() {
		It("holds a deleted ImageMirror, reporting why, until cleanup.enabled is set to false, and leaves its tags in place",
			func() {
				Expect(kubectlStdin(mirrorManifest(refusedMirror, refusingRegistry, "refused", true), "apply", "-f", "-")).
					Error().NotTo(HaveOccurred())
				Eventually(refusingRegistry.tags, "4m").WithArguments("refused/registry.k8s.io/pause").
					Should(ContainElement(pauseTag + "_" + clusterID))

				_, err := kubectl("delete", "imagemirror", refusedMirror, "--wait=false")
				Expect(err).NotTo(HaveOccurred())

				By("holding the mirror with Ready False, reason RegistryDeleteUnsupported")
				// The first try comes within StatusInterval of the deletion: no pod runs a copy, so
				// nothing waits out a backoff.
				Eventually(kubectl).WithArguments("get", "imagemirror", refusedMirror,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status} {.status.conditions[?(@.type=="Ready")].reason}`).
					Should(Equal("False RegistryDeleteUnsupported"))
				Expect(kubectl("get", "imagemirror", refusedMirror,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].message}`)).
					To(ContainSubstring("cleanup.enabled: false"))

				By("emitting ImageDeletionFailed on the mirror, naming the refused tag")
				// The mirror is cluster-scoped: its events land in the default namespace.
				Eventually(kubectl).WithArguments("get", "events", "-A",
					"--field-selector", "reason=ImageDeletionFailed,involvedObject.name="+refusedMirror,
					"-o", "jsonpath={.items[*].message}").
					Should(ContainSubstring("/refused/registry.k8s.io/pause:" + pauseTag + "_" + clusterID))
				Expect(refusingRegistry.tags("refused/registry.k8s.io/pause")).To(ContainElement(pauseTag + "_" + clusterID))

				By("releasing the mirror once cleanup.enabled is false")
				_, err = kubectl("patch", "imagemirror", refusedMirror, "--type=merge",
					"-p", `{"spec":{"cleanup":{"enabled":false}}}`)
				Expect(err).NotTo(HaveOccurred())
				Eventually(kubectl).WithArguments("get", "imagemirror", refusedMirror, "--ignore-not-found", "-o", "name").
					Should(BeEmpty())
				Expect(refusingRegistry.tags("refused/registry.k8s.io/pause")).To(ContainElement(pauseTag + "_" + clusterID))
			})

		It("deletes the tags of a deleted ImageMirror with cleanup.deleteByDigest by their manifest digest, then lets it go",
			func() {
				digest := mirrorManifest(digestMirror, refusingRegistry, "digest", true) +
					"  cleanup:\n    deleteByDigest: true\n"
				Expect(kubectlStdin(digest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
				Eventually(refusingRegistry.tags, "4m").WithArguments("digest/registry.k8s.io/pause").
					Should(ContainElement(pauseTag + "_" + clusterID))

				_, err := kubectl("delete", "imagemirror", digestMirror, "--wait=false")
				Expect(err).NotTo(HaveOccurred())

				By("releasing the mirror once the manifest is deleted by digest")
				Eventually(kubectl).WithArguments("get", "imagemirror", digestMirror, "--ignore-not-found", "-o", "name").
					Should(BeEmpty())
				// The tag and the anchor of the pinned pod point at one manifest: both are gone.
				Expect(refusingRegistry.tags("digest/registry.k8s.io/pause")).To(BeEmpty())
			})
	})

	// No node pulls from the in-cluster destination: these specs check what the webhook admits,
	// never that the pod starts.
	Context("with an Always ImageMirror", func() {
		It("routes a new pod to its copy once the copy exists, recording the rewrite in kuik.enix.io/rewrites", func() {
			routed := alwaysMirrorManifest(routedMirror, deletingRegistry.host()+"/routed/", deletingRegistry.secret, "", false)
			Expect(kubectlStdin(routed, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "imagemirror", routedMirror, "--ignore-not-found")

			By("waiting for the copy of the running pod's image")
			Eventually(deletingRegistry.tags, "4m").WithArguments("routed/registry.k8s.io/pause").
				Should(ContainElement(pauseTag + "_" + clusterID))

			copied := deletingRegistry.host() + "/routed/registry.k8s.io/pause:" + pauseTag + "_" + clusterID + "@" + pauseDigest
			By("waiting until every webhook replica routes to the copy")
			Eventually(mirrorReplicaPatches).WithArguments(mirroredImage).Should(everyReplica(ContainSubstring(copied)))

			Expect(kubectlStdin(mirrorPodManifest("routed", mirroredImage), "create", "-f", "-")).
				Error().NotTo(HaveOccurred())
			// Deleted before the mirror, which a pod running its copy would hold.
			DeferCleanup(kubectl, "delete", "pod", "routed", "-n", mirrorNamespace, "--ignore-not-found")

			Expect(kubectl("get", "pod", "routed", "-n", mirrorNamespace,
				"-o", "jsonpath={.spec.containers[0].image}")).To(Equal(copied))
			Expect(kuikAnnotationsIn(mirrorNamespace, "routed")).To(HaveKeyWithValue("kuik.enix.io/rewrites",
				MatchJSON(fmt.Sprintf(`{"pause":{"by":"ImageMirror/%s","origin":%q,"rewrittenTo":%q,"policy":"Always"}}`,
					routedMirror, mirroredImage, copied))))
		})

		// After the spec above, which shows the webhook routing to an Always mirror.
		It("leaves a new pod on its origin, with no kuik annotation, while its destination does not answer", func() {
			// Its podSelector leaves the running pause pod out: no copy fails before the pod is admitted,
			// so the webhook judges the destination by its own check, never by failedImageCopies.
			down := alwaysMirrorManifest(downMirror, "kuik-e2e-mirror-down.invalid/routed/", deletingRegistry.secret,
				downPodLabel, false)
			Expect(kubectlStdin(down, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "imagemirror", downMirror, "--ignore-not-found")

			By("waiting until every webhook replica offers the mirror")
			// An origin that does not answer either: the replica names the mirror among the resources
			// whose candidates all failed, which tells it holds the mirror.
			Eventually(mirrorReplicaPatches).WithArguments("e2e-mirror-down-origin.invalid/pause:" + pauseTag).
				Should(everyReplica(ContainSubstring("ImageMirror/" + downMirror)))

			By("waiting until every webhook replica leaves the pod on its origin")
			// The first probe of a cold host (TLS, token, redirect) can exceed the webhook timeout.
			Eventually(mirrorReplicaPatches).WithTimeout(30 * time.Second).WithArguments(mirroredImage).
				Should(everyReplica(BeEmpty()))

			Expect(kubectlStdin(labelledMirrorPodManifest("down", mirroredImage, downPodLabel), "create", "-f", "-")).
				Error().NotTo(HaveOccurred())
			DeferCleanup(kubectl, "delete", "pod", "down", "-n", mirrorNamespace, "--ignore-not-found")

			Expect(kubectl("get", "pod", "down", "-n", mirrorNamespace,
				"-o", "jsonpath={.spec.containers[0].image}")).To(Equal(mirroredImage))
			Expect(kuikAnnotationsIn(mirrorNamespace, "down")).To(BeEmpty())
		})

		It("holds a deleted ImageMirror while a live pod runs one of its copies, then deletes its tags once the pod is gone",
			func() {
				held := alwaysMirrorManifest(heldMirror, deletingRegistry.host()+"/held/", deletingRegistry.secret, "", true)
				Expect(kubectlStdin(held, "apply", "-f", "-")).Error().NotTo(HaveOccurred())

				By("routing a new pod to the copy of the running pod's image")
				Eventually(deletingRegistry.tags, "4m").WithArguments("held/registry.k8s.io/pause").
					Should(ContainElement(pauseTag + "_" + clusterID))
				copied := deletingRegistry.host() + "/held/registry.k8s.io/pause:" + pauseTag + "_" + clusterID + "@" + pauseDigest
				Eventually(mirrorReplicaPatches).WithArguments(mirroredImage).Should(everyReplica(ContainSubstring(copied)))
				Expect(kubectlStdin(mirrorPodManifest("held", mirroredImage), "create", "-f", "-")).
					Error().NotTo(HaveOccurred())
				DeferCleanup(kubectl, "delete", "pod", "held", "-n", mirrorNamespace, "--ignore-not-found")
				Expect(kubectl("get", "pod", "held", "-n", mirrorNamespace,
					"-o", "jsonpath={.spec.containers[0].image}")).To(Equal(copied))
				// No node pulls from the in-cluster destination: the pod stays Pending in
				// ImagePullBackOff, which is live, as a pod whose image is being pulled.

				ready := `jsonpath={.status.conditions[?(@.type=="Ready")].status} ` +
					`{.status.conditions[?(@.type=="Ready")].reason}`
				before, err := kubectl("get", "imagemirror", heldMirror, "-o", ready)
				Expect(err).NotTo(HaveOccurred())
				Expect(strings.TrimSpace(before)).NotTo(BeEmpty())

				_, err = kubectl("delete", "imagemirror", heldMirror, "--wait=false")
				Expect(err).NotTo(HaveOccurred())

				By("holding the mirror with DeletionBlocked True, reason PodsRunningCopies, naming the pod")
				Eventually(kubectl).WithArguments("get", "imagemirror", heldMirror,
					"-o", `jsonpath={.status.conditions[?(@.type=="DeletionBlocked")].status} `+
						`{.status.conditions[?(@.type=="DeletionBlocked")].reason}`).
					Should(Equal("True PodsRunningCopies"))
				Expect(kubectl("get", "imagemirror", heldMirror,
					"-o", `jsonpath={.status.conditions[?(@.type=="DeletionBlocked")].message}`)).
					To(ContainSubstring(mirrorNamespace + "/held"))
				Expect(kubectl("get", "imagemirror", heldMirror, "-o", ready)).To(Equal(before))

				By("emitting a Normal WaitingForPods event on the mirror")
				// The mirror is cluster-scoped: its events land in the default namespace.
				Eventually(kubectl).WithArguments("get", "events", "-A",
					"--field-selector", "reason=WaitingForPods,involvedObject.name="+heldMirror,
					"-o", "jsonpath={.items[*].type}").Should(Equal("Normal"))
				Expect(deletingRegistry.tags("held/registry.k8s.io/pause")).To(ContainElement(pauseTag + "_" + clusterID))

				By("releasing the mirror and deleting its tags once the pod is gone")
				_, err = kubectl("delete", "pod", "held", "-n", mirrorNamespace)
				Expect(err).NotTo(HaveOccurred())
				// The pod's deletion wakes the held mirror, sooner than its backoff.
				Eventually(kubectl).WithArguments("get", "imagemirror", heldMirror, "--ignore-not-found", "-o", "name").
					Should(BeEmpty())
				Expect(deletingRegistry.tags("held/registry.k8s.io/pause")).To(BeEmpty())
			})
	})

	// No node pulls from sourceRegistry either: the pods of httpOriginNamespace stay Pending, and
	// these specs check what kuik decides about their origin. go-containerregistry speaks plain HTTP
	// to a private IP on its own: the origin is named through the cluster DNS, so that only the
	// insecure of the entry it matches lets kuik reach it.
	Context("with an origin on a plain HTTP registry addressed by its DNS name", func() {
		var originImage string

		BeforeAll(func() {
			By("deploying the origin registry with its write credential")
			Expect(kubectlStdin(sourceRegistry.manifest(), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			Eventually(kubectl).WithArguments("rollout", "status", "deployment/"+sourceRegistry.name, "-n", registryNamespace,
				"--timeout=30s").Should(ContainSubstring("successfully rolled out"))
			_, err := kubectl("create", "secret", "docker-registry", sourceRegistry.secret, "-n", namespace,
				"--docker-server="+sourceRegistry.host(), "--docker-username="+registryUser,
				"--docker-password="+registryPassword)
			Expect(err).NotTo(HaveOccurred())

			By("seeding it with a copy of the running pause pod's image, left in place")
			seed := mirrorManifest(seedMirror, sourceRegistry, "origin", true) + "  cleanup:\n    enabled: false\n"
			Expect(kubectlStdin(seed, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			Eventually(sourceRegistry.tags, "4m").WithArguments("origin/registry.k8s.io/pause").
				Should(ContainElement(pauseTag + "_" + clusterID))
			// Gone before a pod runs its copy, which would hold it, and before the specs read the
			// origin's requests: its writes there are a destination's, never paced.
			_, err = kubectl("delete", "imagemirror", seedMirror)
			Expect(err).NotTo(HaveOccurred())
			originImage = sourceRegistry.host() + "/origin/registry.k8s.io/pause:" + pauseTag + "_" + clusterID

			By("running a pod on that origin, with its read credential as pull Secret")
			_, err = kubectl("create", "ns", httpOriginNamespace)
			Expect(err).NotTo(HaveOccurred())
			_, err = kubectl("label", "ns", httpOriginNamespace, httpOriginLabel+"=true")
			Expect(err).NotTo(HaveOccurred())
			_, err = kubectl("create", "secret", "docker-registry", httpOriginSecret, "-n", httpOriginNamespace,
				"--docker-server="+sourceRegistry.host(), "--docker-username="+registryUser,
				"--docker-password="+registryPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(kubectlStdin(httpOriginPodManifest("http-origin", originImage), "create", "-f", "-")).
				Error().NotTo(HaveOccurred())
		})

		It("leaves a pod on its origin when the ImageAlternative entry the origin matches is insecure", func() {
			By("failing the origin over HTTPS while the entry is secure")
			// The alternative does not answer either: each replica names the ImageAlternative among
			// the resources whose candidates all failed.
			Expect(kubectlStdin(httpOriginAlternativeManifest(false), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			Eventually(httpOriginReplicaPatches).WithArguments(originImage).
				Should(everyReplica(ContainSubstring("ImageAlternative/" + httpOriginAlternative)))

			By("answering over plain HTTP once the entry is insecure")
			Expect(kubectlStdin(httpOriginAlternativeManifest(true), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			Eventually(httpOriginReplicaPatches).WithArguments(originImage).
				Should(everyReplica(Not(ContainSubstring("kuik.enix.io"))))
		})

		It("reports the origin available when the ImageAlternative entry it matches is insecure", func() {
			Expect(kubectlStdin(httpOriginAlternativeManifest(true), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			Expect(kubectlStdin(httpOriginMonitorManifest, "apply", "-f", "-")).Error().NotTo(HaveOccurred())

			// The reconciler checks it on its own windows, 10s apart on the e2e release.
			Eventually(kubectl).WithTimeout(4*time.Minute).WithArguments("get", "imagemonitor", httpOriginMonitor,
				"-o", "jsonpath={.status.images.origin.available}/{.status.images.origin.tracked}").Should(Equal("1/1"))
		})

		It("spaces the requests the reconciler sends to the origin's host by its requestInterval", func() {
			Expect(kubectlStdin(httpOriginAlternativeManifest(true), "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			before, err := sourceRegistry.requests()
			Expect(err).NotTo(HaveOccurred())

			By("copying the image from the origin")
			paced := selectingMirrorManifest(pacedMirror, httpOriginLabel, deletingRegistry, "paced", true)
			Expect(kubectlStdin(paced, "apply", "-f", "-")).Error().NotTo(HaveOccurred())
			copied := "paced/" + strings.Replace(sourceRegistry.host(), ":", "_", 1) + "/origin/registry.k8s.io/pause"
			Eventually(deletingRegistry.tags, "4m").WithArguments(copied).
				Should(ContainElement(pauseTag + "_" + clusterID + "_" + clusterID))

			By("reading when each request of the reconciler reached the origin since")
			after, err := sourceRegistry.requests()
			Expect(err).NotTo(HaveOccurred())
			output, err := kubectl("get", "pods", "-l", processSelector("reconciler"), "-n", namespace,
				"-o", "jsonpath={.items[*].status.podIP}")
			Expect(err).NotTo(HaveOccurred())
			reconcilers := strings.Fields(output)
			var sent []proxyRequest
			for _, request := range after[len(before):] {
				if slices.Contains(reconcilers, request.Client) {
					sent = append(sent, request)
				}
			}
			slices.SortFunc(sent, func(a, b proxyRequest) int { return a.Start.Compare(b.Start) })
			// The copy's blob reads are among them: the checks of httpOriginMonitor share the series.
			Expect(sent).To(ContainElement(HaveField("URI", ContainSubstring("/blobs/"))))
			Expect(len(sent)).To(BeNumerically(">=", 4))
			// A few milliseconds of network between the reconciler and the proxy, at most.
			const jitter = 100 * time.Millisecond
			for i := 1; i < len(sent); i++ {
				Expect(sent[i].Start.Sub(sent[i-1].Start)).To(BeNumerically(">=", sourceRequestInterval-jitter),
					"%s %s then %s %s", sent[i-1].Method, sent[i-1].URI, sent[i].Method, sent[i].URI)
			}
		})
	})
})

// mirrorReplicaPatches returns, for each webhook pod, the JSON patch it answers for a pod of
// mirrorNamespace running image and labelled downPodLabel, asked directly through the API
// server's proxy. No pod is created.
func mirrorReplicaPatches(image string) (map[string]string, error) {
	responses, err := replicaResponses(mirrorNamespace, labelledMirrorPodManifest("replica-probe", image, downPodLabel))
	if err != nil {
		return nil, err
	}
	patches := map[string]string{}
	for pod, response := range responses {
		patches[pod] = string(response.Patch)
	}
	return patches, nil
}

// mirrorPodManifest is a pod of the mirror namespace running image in one container named
// pause, admissible under the restricted security policy.
func mirrorPodManifest(name, image string) string {
	return labelledMirrorPodManifest(name, image, "")
}

// labelledMirrorPodManifest is mirrorPodManifest with the label `<label>: "true"`, none when
// label is empty.
func labelledMirrorPodManifest(name, image, label string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels: {%s}
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
`, name, mirrorNamespace, trueLabel(label), image)
}

// trueLabel is the YAML flow mapping entry `<label>: "true"`, empty when label is.
func trueLabel(label string) string {
	if label == "" {
		return ""
	}
	return fmt.Sprintf("%q: \"true\"", label)
}

// tags lists the tags the registry holds in repository, read from its storage: an empty list
// when the repository or its tags are gone.
func (r destinationRegistry) tags(repository string) ([]string, error) {
	output, err := kubectl("exec", "-n", registryNamespace, "deploy/"+r.name, "-c", "registry", "--",
		"sh", "-c", fmt.Sprintf("ls %s/%s/_manifests/tags 2>/dev/null || true", registryStorage, repository))
	if err != nil {
		return nil, err
	}
	return strings.Fields(output), nil
}

// proxyRequest is one request the proxy of a registry logged.
type proxyRequest struct {
	// Start is when the proxy read its first bytes
	Start time.Time
	// Client is the IP address it came from
	Client string
	Method string
	URI    string
}

// requests lists the requests the proxy in front of the registry logged, in the order they
// ended. The registry must refuse deletes: only those run behind the proxy.
func (r destinationRegistry) requests() ([]proxyRequest, error) {
	output, err := kubectl("logs", "-n", registryNamespace, "deploy/"+r.name, "-c", "proxy")
	if err != nil {
		return nil, err
	}
	var requests []proxyRequest
	for _, line := range utils.GetNonEmptyLines(output) {
		// The format timed of refusingProxyConfig; nginx's own messages share the stream.
		fields := strings.Fields(line)
		if len(fields) != 6 {
			continue
		}
		end, endErr := strconv.ParseFloat(fields[0], 64)
		took, tookErr := strconv.ParseFloat(fields[1], 64)
		if endErr != nil || tookErr != nil {
			continue
		}
		requests = append(requests, proxyRequest{
			Start:  time.UnixMilli(int64(math.Round((end - took) * 1000))),
			Client: fields[2],
			Method: fields[3],
			URI:    fields[4],
		})
	}
	return requests, nil
}

// httpOriginAlternativeManifest is httpOriginAlternative, offering an alternative that does not
// answer for the image of the httpOrigin pods, its entry for that image marked insecure or not.
// Under OnFailure the origin is tried first.
func httpOriginAlternativeManifest(insecure bool) string {
	return fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageAlternative
metadata:
  name: %s
spec:
  rewritePolicy: OnFailure
  namespaceSelector:
    matchLabels:
      %s: "true"
  alternatives:
    - repository: %s/origin/registry.k8s.io/pause
      insecure: %t
    - repository: kuik-e2e-http-origin.invalid/pause
`, httpOriginAlternative, httpOriginLabel, sourceRegistry.host(), insecure)
}

// httpOriginMonitorManifest is httpOriginMonitor.
var httpOriginMonitorManifest = fmt.Sprintf(`
apiVersion: kuik.enix.io/v1alpha1
kind: ImageMonitor
metadata:
  name: %s
spec:
  namespaceSelector:
    matchLabels:
      %s: "true"
`, httpOriginMonitor, httpOriginLabel)

// httpOriginPodManifest is a pod of httpOriginNamespace running image in one container named
// pause, with the read credential of sourceRegistry as pull Secret.
func httpOriginPodManifest(name, image string) string {
	return pausePodManifest(httpOriginNamespace, name, image) +
		"  imagePullSecrets:\n    - name: " + httpOriginSecret + "\n"
}

// httpOriginReplicaPatches returns, for each webhook pod, the JSON patch it answers for an
// httpOrigin pod running image, asked directly through the API server's proxy. No pod is
// created.
func httpOriginReplicaPatches(image string) (map[string]string, error) {
	responses, err := replicaResponses(httpOriginNamespace, httpOriginPodManifest("replica-probe", image))
	if err != nil {
		return nil, err
	}
	patches := map[string]string{}
	for pod, response := range responses {
		patches[pod] = string(response.Patch)
	}
	return patches, nil
}
