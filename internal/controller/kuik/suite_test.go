/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kuik

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dockerClient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kuikv1alpha1ext1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1ext1"
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
var dockerClientApiVersion = os.Getenv("DOCKER_CLIENT_API_VERSION")

func setupRegistry() {
	client, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithVersion(dockerClientApiVersion))
	Expect(err).NotTo(HaveOccurred())

	// Pull image
	ctx := context.Background()
	reader, err := client.ImagePull(ctx, "registry", image.PullOptions{})
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
	err = client.ContainerStart(ctx, resp.ID, container.StartOptions{})
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
	client, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithVersion(dockerClientApiVersion))
	Expect(err).NotTo(HaveOccurred())

	err = client.ContainerRemove(context.Background(), registryContainerId, container.RemoveOptions{
		Force: true,
	})
	Expect(err).NotTo(HaveOccurred())
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = kuikv1alpha1ext1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	setupRegistry()

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		MetricsBindAddress: ":8081",
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
