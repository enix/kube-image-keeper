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
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	corev1 "k8s.io/api/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc
var registryContainerId string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
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

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stdout), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = kuikv1alpha1.AddToScheme(scheme.Scheme)
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
		Client:      k8sManager.GetClient(),
		ApiReader:   k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("cachedimage-controller"),
		ExpiryDelay: 1 * time.Hour,
	}).SetupWithManager(k8sManager, 3)
	Expect(err).ToNot(HaveOccurred())

	err = (&PodReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	removeRegistry()
})
