//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/utils"
)

// The names below are the ones the chart renders, the only deployment path. Keep them in step
// with helm/kube-image-keeper: `helm template kube-image-keeper helm/kube-image-keeper` lists them.

// namespace the chart is installed in, the default of the deploy task
const namespace = "kuik-system"

// releaseSelector matches every pod of the release, whichever process it runs
const releaseSelector = "app.kubernetes.io/name=kube-image-keeper"

// processes are the three the chart deploys, one Deployment each, named after the
// subcommand they run
var processes = []string{"webhook", "reconciler", "secret-syncer"}

// deploymentName is the Deployment of a process
func deploymentName(process string) string { return "kube-image-keeper-" + process }

// processSelector matches the pods of one process
func processSelector(process string) string {
	return releaseSelector + ",app.kubernetes.io/component=" + process
}

// serviceAccountName is the ServiceAccount a process runs under, one per process
func serviceAccountName(process string) string { return "kube-image-keeper-" + process }

// allNamespaces asks kubectl auth can-i about every namespace at once
const allNamespaces = "*"

// metricsServiceName is the metrics service of a process, each one exporting its own
func metricsServiceName(process string) string { return "kube-image-keeper-" + process + "-metrics" }

// metricsPort is the port the metrics service listens on, in plain HTTP (-metrics-secure=false)
const metricsPort = 8080

// webhookServiceName backs the mutating webhook
const webhookServiceName = "kube-image-keeper-webhook"

// mutatingWebhookName is the MutatingWebhookConfiguration cert-manager injects its CA into
const mutatingWebhookName = "kube-image-keeper-mutating-webhook"

// certSecretName holds the webhook serving certificate issued by cert-manager
const certSecretName = "kube-image-keeper-webhook-server-cert"

var _ = Describe("Manager", Ordered, func() {
	var webhookPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching the logs of every process")
			cmd := exec.Command("kubectl", "logs", "-l", releaseSelector, "-n", namespace,
				"--prefix", "--tail=200")
			releaseLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Process logs:\n %s", releaseLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get the process logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching the description of every pod of the release")
			cmd = exec.Command("kubectl", "describe", "pods", "-l", releaseSelector, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod descriptions:\n", podDescription)
			} else {
				fmt.Println("Failed to describe the pods of the release")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run the three processes the chart deploys", func() {
			for _, process := range processes {
				By("waiting for the " + process + " Deployment to become available")
				verifyDeploymentAvailable := func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "deployment", deploymentName(process), "-n", namespace,
						"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}")
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Deployment of the "+process+" should exist")
					g.Expect(output).To(Equal("True"), "Deployment of the "+process+" is not available")
				}
				Eventually(verifyDeploymentAvailable).Should(Succeed())
			}

			By("getting the name of the webhook pod, the one the later specs read")
			verifyWebhookPodRunning := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", processSelector("webhook"),
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve the webhook pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(2), "expected the 2 webhook replicas the chart deploys by default")
				webhookPodName = podNames[0]
				g.Expect(webhookPodName).To(ContainSubstring(deploymentName("webhook")))
			}
			Eventually(verifyWebhookPodRunning).Should(Succeed())
		})

		It("should run each process under its own ServiceAccount", func() {
			for _, process := range processes {
				By("checking the ServiceAccount of the " + process + " exists")
				cmd := exec.Command("kubectl", "get", "serviceaccount", serviceAccountName(process), "-n", namespace)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "ServiceAccount of the "+process+" should exist")

				By("checking the " + process + " Deployment runs under it")
				cmd = exec.Command("kubectl", "get", "deployment", deploymentName(process), "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.serviceAccountName}")
				output, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).To(Equal(serviceAccountName(process)))
			}
		})

		// The permissions of docs/v3/architecture.md, "Permissions": one entry per cell of the
		// table that carries a property of the design, under the default secretAccess.mode
		// (permissive). The rules grant get, list and watch together, so one verb stands for the
		// three. resource may carry a subresource after a slash, and ns is empty for a
		// cluster-scoped resource. The secret syncer has no loop yet, so it holds none of the
		// permissions its markers will bring (notes/0009): its entries flip with them.
		DescribeTable("should hold exactly the permissions architecture.md grants",
			func(process, verb, resource, ns string, allowed bool) {
				Expect(canI(process, verb, resource, ns)).To(Equal(allowed))
			},
			// imagealternatives, imagemirrors, imagemonitors: get, list, watch for the three, no create or delete
			Entry("the webhook reads the ImageMirrors", "webhook", "get", "imagemirrors", allNamespaces, true),
			Entry("the reconciler reads the ImageAlternatives", "reconciler", "get", "imagealternatives", allNamespaces, true),
			Entry("the secret syncer reads no ImageMirror yet, it has no loop",
				"secret-syncer", "get", "imagemirrors", allNamespaces, false),
			Entry("the webhook cannot create an ImageMirror", "webhook", "create", "imagemirrors", allNamespaces, false),
			Entry("the webhook cannot delete an ImageMirror", "webhook", "delete", "imagemirrors", allNamespaces, false),
			Entry("the reconciler cannot update an ImageMirror, only its status",
				"reconciler", "update", "imagemirrors", allNamespaces, false),

			// .../status: update, patch for the reconciler only
			Entry("the webhook cannot write a status", "webhook", "patch", "imagemirrors/status", allNamespaces, false),
			Entry("the reconciler writes the status of an ImageMonitor",
				"reconciler", "patch", "imagemonitors/status", allNamespaces, true),
			Entry("the secret syncer cannot write a status",
				"secret-syncer", "patch", "imagemirrors/status", allNamespaces, false),

			// pods: none for the webhook, get, list, watch for the other two
			Entry("the webhook does not read pods, the AdmissionReview carries them",
				"webhook", "get", "pods", allNamespaces, false),
			Entry("the reconciler reads the pods", "reconciler", "list", "pods", allNamespaces, true),

			// namespaces: get, list, watch for the three
			Entry("the webhook reads the namespaces, for namespaceSelector", "webhook", "list", "namespaces", "", true),
			Entry("the reconciler reads the namespaces", "reconciler", "list", "namespaces", "", true),

			// secrets in the install namespace: get, list, watch for the three
			Entry("the webhook reads the Secrets of the install namespace", "webhook", "get", "secrets", namespace, true),
			Entry("the reconciler reads the Secrets of the install namespace",
				"reconciler", "get", "secrets", namespace, true),
			Entry("the secret syncer reads no Secret of the install namespace yet, it has no loop",
				"secret-syncer", "get", "secrets", namespace, false),

			// secrets cluster-wide, read: permissive only for the webhook and the reconciler, never the syncer
			Entry("the webhook reads the Secrets of an application namespace in permissive mode",
				"webhook", "get", "secrets", "default", true),
			Entry("the reconciler reads the Secrets of an application namespace in permissive mode",
				"reconciler", "get", "secrets", "default", true),
			Entry("the secret syncer reads no Secret of an application namespace, whatever the mode",
				"secret-syncer", "get", "secrets", "default", false),

			// secrets cluster-wide, write: create, patch for the syncer only, never delete
			Entry("the webhook cannot create a Secret", "webhook", "create", "secrets", allNamespaces, false),
			Entry("the reconciler cannot create a Secret", "reconciler", "create", "secrets", allNamespaces, false),
			Entry("the secret syncer creates no Secret yet, its writes ship with its loop",
				"secret-syncer", "create", "secrets", allNamespaces, false),
			Entry("the secret syncer cannot delete a Secret", "secret-syncer", "delete", "secrets", allNamespaces, false),

			// serviceaccounts/token: create for the syncer, for auth.provider; nobody until its loop
			Entry("the secret syncer requests no ServiceAccount token yet, it has no loop",
				"secret-syncer", "create", "serviceaccounts/token", allNamespaces, false),

			// events: create, patch for the reconciler and the syncer
			Entry("the webhook records no event", "webhook", "create", "events", namespace, false),
			Entry("the reconciler records events", "reconciler", "create", "events", namespace, true),
			Entry("the secret syncer records events", "secret-syncer", "create", "events", namespace, true),

			// leases: leader election for the reconciler and the syncer, in the install namespace
			Entry("the webhook holds no lease, it is not elected", "webhook", "update", "leases", namespace, false),
			Entry("the reconciler holds its lease", "reconciler", "update", "leases", namespace, true),
			Entry("the reconciler holds no lease outside the install namespace",
				"reconciler", "update", "leases", "default", false),
			Entry("the secret syncer holds its lease", "secret-syncer", "update", "leases", namespace, true),

			// nodes: nobody in v3.0
			Entry("no process reads the nodes", "reconciler", "get", "nodes", "", false),
		)

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("validating that each process has its own metrics service")
			for _, process := range processes {
				cmd := exec.Command("kubectl", "get", "service", metricsServiceName(process), "-n", namespace)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Metrics service of the "+process+" should exist")
			}

			By("ensuring the webhook pod is ready")
			verifyWebhookPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", webhookPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Webhook pod not ready")
			}
			Eventually(verifyWebhookPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the webhook is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", webhookPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			By("waiting for the webhook service endpoints to be ready")
			verifyWebhookEndpointsReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpointslices.discovery.k8s.io", "-n", namespace,
					"-l", "kubernetes.io/service-name="+webhookServiceName,
					"-o", "jsonpath={range .items[*]}{range .endpoints[*]}{.addresses[*]}{end}{end}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Webhook endpoints should exist")
				g.Expect(output).ShouldNot(BeEmpty(), "Webhook endpoints not yet ready")
			}
			Eventually(verifyWebhookEndpointsReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying the mutating webhook server is ready")
			verifyMutatingWebhookReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "mutatingwebhookconfigurations.admissionregistration.k8s.io",
					mutatingWebhookName,
					"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "MutatingWebhookConfiguration should exist")
				g.Expect(output).ShouldNot(BeEmpty(), "Mutating webhook CA bundle not yet injected")
			}
			Eventually(verifyMutatingWebhookReady, 3*time.Minute, time.Second).Should(Succeed())

			By("waiting additional time for webhook server to stabilize")
			time.Sleep(5 * time.Second)

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd := exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v http://%s.%s.svc.cluster.local:%d/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}]
					}
				}`, metricsServiceName("webhook"), namespace, metricsPort))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		It("should provisioned cert-manager", func() {
			By("validating that cert-manager has the certificate Secret")
			verifyCertManager := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secrets", certSecretName, "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyCertManager).Should(Succeed())
		})

		It("should have CA injection for mutating webhooks", func() {
			By("checking CA injection for mutating webhooks")
			verifyCAInjection := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"mutatingwebhookconfigurations.admissionregistration.k8s.io",
					mutatingWebhookName,
					"-o", "go-template={{ range .webhooks }}{{ .clientConfig.caBundle }}{{ end }}")
				mwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(mwhOutput)).To(BeNumerically(">", 10))
			}
			Eventually(verifyCAInjection).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// canI asks the API server whether the ServiceAccount of process may perform verb on resource
// in ns, allNamespaces for every namespace, empty for a cluster-scoped resource. resource may
// name a subresource after a slash.
func canI(process, verb, resource, ns string) (bool, error) {
	args := []string{"auth", "can-i", verb}
	name, subresource, found := strings.Cut(resource, "/")
	args = append(args, name)
	if found {
		args = append(args, "--subresource", subresource)
	}
	switch ns {
	case "":
	case allNamespaces:
		args = append(args, "--all-namespaces")
	default:
		args = append(args, "-n", ns)
	}
	args = append(args, "--as", "system:serviceaccount:"+namespace+":"+serviceAccountName(process))

	// can-i exits 1 when the answer is no, so the answer is read from the output rather than
	// from the error.
	output, err := utils.Run(exec.Command("kubectl", args...))
	lines := utils.GetNonEmptyLines(output)
	if len(lines) > 0 {
		switch strings.TrimSpace(lines[len(lines)-1]) {
		case "yes":
			return true, nil
		case "no":
			return false, nil
		}
	}

	return false, fmt.Errorf("unexpected answer from kubectl auth can-i: %q (%v)", output, err)
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}
