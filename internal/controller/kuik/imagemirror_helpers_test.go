package kuik

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
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
// destination is scanned every hour.
func mirrorConfig() *config.Config {
	minute := func() *config.Window {
		return &config.Window{Interval: &config.Duration{Duration: time.Minute}, Timeout: &config.Duration{Duration: time.Minute}}
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

// secretAuth names a Secret of the install namespace as a credential.
func secretAuth(name string) *kuikv1alpha1.DestinationCredentials {
	return &kuikv1alpha1.DestinationCredentials{Auth: kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}}
}
