package kuik

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikregistry "github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const mirrorClusterID = "cluster-a"

// mirrorHarness runs an ImageMirrorReconciler against an in-memory destination registry, its
// copies and drift checks paced by a scheduler on a fake clock.
type mirrorHarness struct {
	name        string
	metrics     *prometheus.Registry
	recorder    *eventRecorder
	clock       *clocktesting.FakeClock
	config      *config.Config
	scheduler   *pacing.Scheduler
	destination *registrytest.Registry
	reconciler  *ImageMirrorReconciler
}

// mirrorConfig is a config whose source registries open a window every minute, and whose
// destination is scanned every hour. Its timeouts are 0, no deadline: they count on the fake
// clock, so moving it to the next window would otherwise abandon a copy still running.
func mirrorConfig() *config.Config {
	minute := func() *config.Window {
		return &config.Window{Interval: &config.Duration{Duration: time.Minute}, Timeout: &config.Duration{}}
	}
	return &config.Config{
		ClusterID:  mirrorClusterID,
		Registries: config.Registries{Default: config.RegistryPacing{Check: minute(), Copy: minute()}},
		Mirror:     config.Mirror{DestinationScan: config.DestinationScan{Interval: config.Duration{Duration: time.Hour}}},
	}
}

// newMirrorHarness builds a reconciler holding the lease, and stops its scheduler when the
// spec ends.
func newMirrorHarness() *mirrorHarness {
	createNamespace(installNamespace, nil)
	h := &mirrorHarness{
		name:        unique("im"),
		metrics:     prometheus.NewRegistry(),
		recorder:    &eventRecorder{},
		clock:       clocktesting.NewFakeClock(time.Now()),
		config:      mirrorConfig(),
		destination: registrytest.New(),
	}
	DeferCleanup(h.destination.Close)
	h.scheduler = pacing.New(h.clock, h.config)
	schedulerCtx, stop := context.WithCancel(ctx)
	go func() { _ = h.scheduler.Start(schedulerCtx) }()
	DeferCleanup(stop)

	var err error
	h.reconciler, err = NewImageMirrorReconciler(k8sClient, k8sClient.Scheme(), ImageMirrorOptions{
		APIReader:                k8sClient,
		ClusterResourceNamespace: installNamespace,
		Recorder:                 h.recorder,
		Registerer:               h.metrics,
		Scheduler:                h.scheduler,
		Registry:                 kuikregistry.NewClient(),
		Config:                   h.config,
		Clock:                    h.clock,
	})
	Expect(err).NotTo(HaveOccurred())
	h.reconciler.Elected(h.clock.Now().Add(-time.Minute))
	return h
}

// mirror creates the ImageMirror under test, copying to the in-memory destination and
// selecting the namespaces labelled with its own name.
func (h *mirrorHarness) mirror(mutate ...func(*kuikv1alpha1.ImageMirror)) {
	im := &kuikv1alpha1.ImageMirror{
		ObjectMeta: metav1.ObjectMeta{Name: h.name},
		Spec: kuikv1alpha1.ImageMirrorSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{testLabel: h.name}},
			Destination:       kuikv1alpha1.MirrorDestination{Path: h.destination.Host() + "/mirror/", Insecure: true},
		},
	}
	for _, m := range mutate {
		m(im)
	}
	Expect(k8sClient.Create(ctx, im)).To(Succeed())
}

// namespace creates a namespace the mirror under test selects.
func (h *mirrorHarness) namespace() string {
	ns := unique("ns")
	createNamespace(ns, map[string]string{testLabel: h.name})
	return ns
}

// reconcile runs one reconcile of the mirror under test.
func (h *mirrorHarness) reconcile() reconcile.Result {
	GinkgoHelper()
	result, err := h.reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: h.name}})
	Expect(err).NotTo(HaveOccurred())
	return result
}

// status reads the status of the mirror under test.
func (h *mirrorHarness) status() kuikv1alpha1.ImageMirrorStatus {
	GinkgoHelper()
	var im kuikv1alpha1.ImageMirror
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: h.name}, &im)).To(Succeed())
	return im.Status
}

// condition reads one condition of the mirror under test, nil when absent.
func (h *mirrorHarness) condition(conditionType string) *metav1.Condition {
	GinkgoHelper()
	return meta.FindStatusCondition(h.status().Conditions, conditionType)
}

// source starts a source registry, closed when the spec ends.
func (h *mirrorHarness) source(opts ...registrytest.Option) *registrytest.Registry {
	reg := registrytest.New(opts...)
	DeferCleanup(reg.Close)
	return reg
}

// window moves the clock to the next window of every host paced every minute.
func (h *mirrorHarness) window() {
	h.clock.Step(time.Minute)
}

// fallBack runs the window of the origin host, which the origin fails to answer, then
// reconciles until the reference waits in the copy queue of alternative.
func (h *mirrorHarness) fallBack(alternative *registrytest.Registry) {
	h.reconcile()
	h.window()
	Eventually(func() bool {
		h.reconcile()
		return h.queued(alternative)
	}).Should(BeTrue())
}

// queued reports whether the scheduler holds a copy queue on the host of reg: it then
// exposes the copy pace of that host.
func (h *mirrorHarness) queued(reg *registrytest.Registry) bool {
	scheduling := prometheus.NewRegistry()
	scheduling.MustRegister(h.scheduler.Collector())
	_, ok := gauge(scheduling, "kuik_registry_interval_seconds", map[string]string{"registry": reg.Host(), "operation": "Copy"})
	return ok
}

// destinationTag is the destination reference, relative to the destination registry, that
// the mirror under test writes for the tagged reference repository:v1 of source.
func destinationTag(source *registrytest.Registry, repository string) string {
	return "mirror/" + strings.Replace(source.Host(), ":", "_", 1) + "/" + repository + ":v1_" + mirrorClusterID
}

// destinationRepository is the destination repository of repository of source, as
// status.repositories lists it.
func (h *mirrorHarness) destinationRepository(source *registrytest.Registry, repository string) string {
	return h.destination.Host() + "/mirror/" + strings.Replace(source.Host(), ":", "_", 1) + "/" + repository
}

// copied reports whether the destination holds ref, relative to it.
func (h *mirrorHarness) copied(ref string) func() error {
	return func() error {
		_, err := h.destination.Head(ref)
		return err
	}
}

// staticPod creates a pod running image that the kubelet published for a static pod.
func staticPod(namespace, image string) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: unique("static"), Namespace: namespace,
			Annotations: map[string]string{"kubernetes.io/config.mirror": "hash"},
		},
		// The API server only accepts a mirror pod bound to the node that publishes it.
		Spec: corev1.PodSpec{NodeName: "node-a", Containers: []corev1.Container{{Name: appContainer, Image: image}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
}

// rejectPushes makes reg refuse every manifest it is asked to write, until accept.
func rejectPushes(reg *registrytest.Registry) {
	reg.Intercept(registrytest.Status(http.MethodPut, "/manifests/", http.StatusBadRequest, nil))
}

// acceptPushes lets reg answer every request again.
func acceptPushes(reg *registrytest.Registry) {
	reg.Intercept(func(http.ResponseWriter, *http.Request) bool { return false })
}

// createCredentials creates a docker-registry Secret of the install namespace holding user
// and password for host, and returns its name.
func createCredentials(host, user, password string) string {
	name := unique("creds")
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	dockerConfig := fmt.Sprintf(`{"auths":{%q:{"username":%q,"password":%q,"auth":%q}}}`, host, user, password, auth)
	Expect(k8sClient.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: installNamespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerConfig)},
	})).To(Succeed())
	return name
}

// alternativeTo creates an ImageAlternative selecting the namespaces of the mirror under
// test, whose entries are the repository acme/app on each registry, in order.
func (h *mirrorHarness) alternativeTo(entries ...kuikv1alpha1.Alternative) {
	Expect(k8sClient.Create(ctx, &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: unique("ia")},
		Spec: kuikv1alpha1.ImageAlternativeSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{testLabel: h.name}},
			Alternatives:      entries,
		},
	})).To(Succeed())
}

// appEntry is the entry naming the repository acme/app on reg.
func appEntry(reg *registrytest.Registry) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{Repository: reg.Host() + "/acme/app", Insecure: true}
}

// secretAuth names a Secret of the install namespace as a credential.
func secretAuth(name string) *kuikv1alpha1.DestinationCredentials {
	return &kuikv1alpha1.DestinationCredentials{Auth: kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}}
}
