package controllers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	dockerClient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	corev1 "k8s.io/api/core/v1"

	kuikenixiov1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment
var registryContainerId string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

func setupRegistry() {
	client, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv)
	Expect(err).NotTo(HaveOccurred())

	// Pull image
	ctx := context.Background()
	reader, err := client.ImagePull(ctx, "registry", types.ImagePullOptions{})
	Expect(err).NotTo(HaveOccurred())
	_, err = io.Copy(os.Stdout, reader)
	Expect(err).NotTo(HaveOccurred())
	err = reader.Close()
	Expect(err).NotTo(HaveOccurred())

	// Create container
	resp, err := client.ContainerCreate(ctx, &container.Config{
		Image:        "registry",
		ExposedPorts: nat.PortSet{"5000": struct{}{}},
		Env: []string{
			"REGISTRY_STORAGE_DELETE_ENABLED=true",
		},
	}, &container.HostConfig{
		PublishAllPorts: true,
	}, nil, nil, "")
	Expect(err).NotTo(HaveOccurred())
	registryContainerId = resp.ID

	// Start container
	err = client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Configure registry endpoint
	containerJson, err := client.ContainerInspect(ctx, registryContainerId)
	Expect(err).NotTo(HaveOccurred())

	portMap := containerJson.NetworkSettings.Ports["5000/tcp"]
	Expect(portMap).NotTo(BeNil())
	Expect(portMap).NotTo(HaveLen(0))

	dockerHostname := os.Getenv("DOCKER_HOSTNAME")
	if dockerHostname == "" {
		dockerHostname = "localhost"
	}

	registry.Endpoint = dockerHostname + ":" + portMap[0].HostPort
}

func removeRegistry() {
	client, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv)
	Expect(err).NotTo(HaveOccurred())

	err = client.ContainerRemove(context.Background(), registryContainerId, types.ContainerRemoveOptions{
		Force: true,
	})
	Expect(err).NotTo(HaveOccurred())
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stdout), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = kuikenixiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	setupRegistry()

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&CachedImageReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("cachedimage-controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PodReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		ExpiryDelay: 1 * time.Hour,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	removeRegistry()
})
