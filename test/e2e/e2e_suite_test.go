//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/utils"
)

// kubectlTimeout bounds one kubectl call and makeTimeout one make target: utils.Run waits for
// the process, and Eventually never interrupts a poll that hangs, so an unbounded call against
// an API server that stopped answering would stall the suite instead of failing it.
const (
	kubectlTimeout = time.Minute
	makeTimeout    = 10 * time.Minute
)

// orphanPipeDelay is how long a killed command may keep its output open: make leaves its
// children (task, helm, docker) holding the pipes, and the output is only read once they close.
const orphanPipeDelay = 10 * time.Second

// runBounded runs name with args through utils.Run, feeding it stdin when not nil, and kills it
// once timeout has elapsed.
func runBounded(timeout time.Duration, stdin io.Reader, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.WaitDelay = orphanPipeDelay
	return utils.Run(cmd)
}

// kubectlCommand is a kubectl command killed once kubectlTimeout has elapsed, for the callers
// that build the command before handing it to utils.Run.
func kubectlCommand(args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	// The context is released at its deadline, whether the command is still running or not.
	time.AfterFunc(kubectlTimeout, cancel)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.WaitDelay = orphanPipeDelay
	return cmd
}

// kubectl runs kubectl with args, bounded by kubectlTimeout.
func kubectl(args ...string) (string, error) {
	return runBounded(kubectlTimeout, nil, "kubectl", args...)
}

// makeTarget runs make with args, bounded by makeTimeout.
func makeTarget(args ...string) (string, error) {
	return runBounded(makeTimeout, nil, "make", args...)
}

var (
	// managerImage is the manager image to be built and loaded for testing.
	managerImage = "example.com/kube-image-keeper:v0.0.1"
	// shouldCleanupCertManager tracks whether CertManager was installed by this suite.
	shouldCleanupCertManager = false
)

// TestE2E runs the e2e test suite to validate the solution in an isolated environment.
// The default setup requires Kind and CertManager.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
// By default, kuberc is disabled to ensure consistent test behavior across different environments.
// To skip CertManager installation, set: CERT_MANAGER_INSTALL_SKIP=true
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kube-image-keeper e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("building the manager image")
	_, err := makeTarget("docker-build", fmt.Sprintf("IMG=%s", managerImage))
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	// TODO(user): If you want to change the e2e test vendor from Kind,
	// ensure the image is built and available, then remove the following block.
	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	configureKubectlKubeRC()
	setupCertManager()
	deployChart()
})

var _ = AfterSuite(func() {
	undeployChart()
	teardownCertManager()
})

// After each failed spec, collect the logs, events and pod descriptions of the release for
// debugging.
var _ = AfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}
	By("Fetching the logs of every process")
	releaseLogs, err := kubectl("logs", "-l", releaseSelector, "-n", namespace, "--prefix", "--tail=200")
	if err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Process logs:\n %s", releaseLogs)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get the process logs: %s", err)
	}

	By("Fetching Kubernetes events")
	eventsOutput, err := kubectl("get", "events", "-A", "--sort-by=.lastTimestamp")
	if err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
	}

	By("Fetching curl-metrics logs")
	metricsOutput, err := kubectl("logs", "curl-metrics", "-n", namespace)
	if err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
	}

	By("Fetching the description of every pod of the release")
	podDescription, err := kubectl("describe", "pods", "-l", releaseSelector, "-n", namespace)
	if err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Pod descriptions:\n%s", podDescription)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to describe the pods of the release: %s", err)
	}
})

// deployChart creates the install namespace under the restricted security policy, installs
// the CRDs and deploys the chart, once for every spec of the suite: Ginkgo shuffles the
// top-level containers, so none of them may own the release.
func deployChart() {
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	By("creating manager namespace")
	_, err := kubectl("create", "ns", namespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

	By("labeling the namespace to enforce the restricted security policy")
	_, err = kubectl("label", "--overwrite", "ns", namespace, "pod-security.kubernetes.io/enforce=restricted")
	Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

	By("installing CRDs")
	_, err = makeTarget("install")
	Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

	By("deploying the controller-manager")
	_, err = makeTarget("deploy", fmt.Sprintf("IMG=%s", managerImage))
	Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
}

// undeployChart undeploys the chart, uninstalls the CRDs and deletes the install namespace.
func undeployChart() {
	By("undeploying the controller-manager")
	_, _ = makeTarget("undeploy")

	By("uninstalling CRDs")
	_, _ = makeTarget("uninstall")

	By("removing manager namespace")
	_, _ = kubectl("delete", "ns", namespace)
}

// Disable kubectl kuberc by default for test isolation.
// This prevents local kubectl configurations from affecting test behavior.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
		_, _ = fmt.Fprintf(GinkgoWriter,
			"kubectl kuberc disabled for consistent test behavior (override with KUBECTL_KUBERC=true)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl kuberc enabled (KUBECTL_KUBERC=true)\n")
	}
}

// setupCertManager installs CertManager if needed for webhook tests.
// Skips installation if CERT_MANAGER_INSTALL_SKIP=true or if already present.
func setupCertManager() {
	if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager installation (CERT_MANAGER_INSTALL_SKIP=true)\n")
		return
	}

	By("checking if CertManager is already installed")
	if utils.IsCertManagerCRDsInstalled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "CertManager is already installed. Skipping installation.\n")
		return
	}

	// Mark for cleanup before installation to handle interruptions and partial installs.
	shouldCleanupCertManager = true

	By("installing CertManager")
	Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
}

// teardownCertManager uninstalls CertManager if it was installed by setupCertManager.
// This ensures we only remove what we installed.
func teardownCertManager() {
	if !shouldCleanupCertManager {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager cleanup (not installed by this suite)\n")
		return
	}

	By("uninstalling CertManager")
	utils.UninstallCertManager()
}
