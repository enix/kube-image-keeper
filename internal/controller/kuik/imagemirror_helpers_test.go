package kuik

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ggcrname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	kuikregistry "github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	mirrorClusterID = "cluster-a"
	labelRegistry   = "registry"
	labelReason     = "reason"
	labelImage      = "image"
	labelOperation  = "operation"
	// statusSubResource is the subresource a status write goes to.
	statusSubResource = "status"
)

// mirrorHarness runs an ImageMirrorReconciler against an in-memory destination registry, its
// copies and drift checks paced by a scheduler on a fake clock.
type mirrorHarness struct {
	name      string
	metrics   *prometheus.Registry
	recorder  *eventRecorder
	clock     *clocktesting.FakeClock
	config    *config.Config
	scheduler *pacing.Scheduler
	// stop stops the scheduler, as a process ending would.
	stop        context.CancelFunc
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
		destination: registrytest.New(registrytest.WithUntagOnDigestDelete()),
	}
	DeferCleanup(h.destination.Close)
	h.scheduler = pacing.New(h.clock, h.config)
	schedulerCtx, stop := context.WithCancel(ctx)
	go func() { _ = h.scheduler.Start(schedulerCtx) }()
	h.stop = stop
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
		ClusterID:                mirrorClusterID,
		Clock:                    h.clock,
	})
	Expect(err).NotTo(HaveOccurred())
	h.reconciler.Elected(h.clock.Now().Add(-time.Minute))
	return h
}

// withListCapacity builds the reconciler again with anomaly lists capped at capacity, so that a
// handful of entries exceeds the cap. Its series go to a new registry.
func (h *mirrorHarness) withListCapacity(capacity int) {
	GinkgoHelper()
	h.metrics = prometheus.NewRegistry()
	var err error
	h.reconciler, err = NewImageMirrorReconciler(k8sClient, k8sClient.Scheme(), ImageMirrorOptions{
		APIReader:                k8sClient,
		ClusterResourceNamespace: installNamespace,
		Recorder:                 h.recorder,
		Registerer:               h.metrics,
		Scheduler:                h.scheduler,
		Registry:                 kuikregistry.NewClient(),
		Config:                   h.config,
		ClusterID:                mirrorClusterID,
		Clock:                    h.clock,
		ListCapacity:             capacity,
	})
	Expect(err).NotTo(HaveOccurred())
	h.reconciler.Elected(h.clock.Now().Add(-time.Minute))
}

// restart replaces the scheduler and the reconciler with new ones on the same objects, as a
// new process would: nothing of the previous memory survives. Its series go to a new registry.
func (h *mirrorHarness) restart() {
	GinkgoHelper()
	h.stop()
	h.scheduler = pacing.New(h.clock, h.config)
	schedulerCtx, stop := context.WithCancel(ctx)
	go func() { _ = h.scheduler.Start(schedulerCtx) }()
	h.stop = stop
	DeferCleanup(stop)
	h.metrics = prometheus.NewRegistry()
	var err error
	h.reconciler, err = NewImageMirrorReconciler(k8sClient, k8sClient.Scheme(), ImageMirrorOptions{
		APIReader:                k8sClient,
		ClusterResourceNamespace: installNamespace,
		Recorder:                 h.recorder,
		Registerer:               h.metrics,
		Scheduler:                h.scheduler,
		Registry:                 kuikregistry.NewClient(),
		Config:                   h.config,
		ClusterID:                mirrorClusterID,
		Clock:                    h.clock,
	})
	Expect(err).NotTo(HaveOccurred())
	h.reconciler.Elected(h.clock.Now())
}

// withStatusConflict builds the reconciler again on a client whose next status write of an
// ImageMirror fails with a conflict whenever conflict is set, which that write clears. Its
// series go to a new registry.
func (h *mirrorHarness) withStatusConflict(conflict *atomic.Bool) {
	GinkgoHelper()
	watching, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())
	c := interceptor.NewClient(watching, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, ok := obj.(*kuikv1alpha1.ImageMirror); ok && subResource == statusSubResource && conflict.CompareAndSwap(true, false) {
				return apierrors.NewConflict(kuikv1alpha1.GroupVersion.WithResource("imagemirrors").GroupResource(), obj.GetName(), errors.New("injected"))
			}
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	})
	h.rebuildOn(c)
}

// withStatusWrites builds the reconciler again on a client that counts its status writes of an
// ImageMirror in writes, no-op ones included: the API server keeps the resourceVersion of a
// write that changes nothing once stored. Its series go to a new registry.
func (h *mirrorHarness) withStatusWrites(writes *atomic.Int32) {
	GinkgoHelper()
	watching, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())
	c := interceptor.NewClient(watching, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, ok := obj.(*kuikv1alpha1.ImageMirror); ok && subResource == statusSubResource {
				writes.Add(1)
			}
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	})
	h.rebuildOn(c)
}

// rebuildOn builds the reconciler again on c, holding the lease. Its series go to a new
// registry.
func (h *mirrorHarness) rebuildOn(c client.Client) {
	GinkgoHelper()
	h.metrics = prometheus.NewRegistry()
	var err error
	h.reconciler, err = NewImageMirrorReconciler(c, k8sClient.Scheme(), ImageMirrorOptions{
		APIReader:                k8sClient,
		ClusterResourceNamespace: installNamespace,
		Recorder:                 h.recorder,
		Registerer:               h.metrics,
		Scheduler:                h.scheduler,
		Registry:                 kuikregistry.NewClient(),
		Config:                   h.config,
		ClusterID:                mirrorClusterID,
		Clock:                    h.clock,
	})
	Expect(err).NotTo(HaveOccurred())
	h.reconciler.Elected(h.clock.Now().Add(-time.Minute))
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

// msgReleasedWhilePullingAlways is the message of the line a deleted mirror logs when it is
// released while live pods run its copies with imagePullPolicy Always.
const msgReleasedWhilePullingAlways = "Released deleted ImageMirror while live pods pull its copies with imagePullPolicy Always"

// lockedBuffer is a buffer a logger may write to from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// reconcileLogged runs one reconcile of the mirror under test and returns every line it logs at
// Info, each decoded from its JSON form.
func (h *mirrorHarness) reconcileLogged() []map[string]any {
	GinkgoHelper()
	out := &lockedBuffer{}
	log := zap.New(zap.WriteTo(out), zap.JSONEncoder())
	_, err := h.reconciler.Reconcile(logf.IntoContext(ctx, log), reconcile.Request{NamespacedName: types.NamespacedName{Name: h.name}})
	Expect(err).NotTo(HaveOccurred())
	var lines []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		Expect(json.Unmarshal([]byte(line), &entry)).To(Succeed(), "line %q", line)
		lines = append(lines, entry)
	}
	return lines
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

// pass moves the clock to the next destination pass.
func (h *mirrorHarness) pass() {
	h.clock.Step(h.config.Mirror.DestinationScan.Interval.Duration)
}

// passNow makes the next reconcile run a destination pass without moving the clock, so that no
// copy window opens with it.
func (h *mirrorHarness) passNow() {
	st := h.reconciler.state(h.name)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastPass = time.Time{}
}

// blockOn makes the destination hold every request matching method and pathPart until release
// is called, and returns a channel closed once the first one arrived. Release runs when the
// spec ends too.
func (h *mirrorHarness) blockOn(method, pathPart string) (entered chan struct{}, release func()) {
	entered, released := make(chan struct{}), make(chan struct{})
	var enter, done sync.Once
	release = func() { done.Do(func() { close(released) }) }
	DeferCleanup(release)
	h.destination.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Method == method && strings.Contains(r.URL.Path, pathPart) {
			enter.Do(func() { close(entered) })
			<-released
		}
		return false
	})
	return entered, release
}

// deleteManifest deletes the manifest ref, relative to the destination, and the tags pointing
// at it, as an external garbage collection would.
func (h *mirrorHarness) deleteManifest(ref string) {
	GinkgoHelper()
	Expect(remote.Delete(h.manifestOf(ref))).To(Succeed())
}

// manifestOf is the digest reference of the manifest the tag ref, relative to the destination,
// points at.
func (h *mirrorHarness) manifestOf(ref string) ggcrname.Digest {
	GinkgoHelper()
	descriptor, err := h.destination.Head(ref)
	Expect(err).NotTo(HaveOccurred())
	repository, _, _ := strings.Cut(ref, ":")
	digest, err := ggcrname.NewDigest(h.destination.Host()+"/"+repository+"@"+descriptor.Digest.String(), ggcrname.Insecure)
	Expect(err).NotTo(HaveOccurred())
	return digest
}

// manifestGone reports whether the destination no longer holds the manifest digest.
func (h *mirrorHarness) manifestGone(digest ggcrname.Digest) bool {
	GinkgoHelper()
	_, err := h.destination.Head(strings.TrimPrefix(digest.String(), h.destination.Host()+"/"))
	return err != nil
}

// refuseTagDeletion answers a DELETE by tag with status, as Harbor answers 400 UNSUPPORTED,
// and lets a DELETE by digest through.
func refuseTagDeletion(status int) registrytest.Interceptor {
	return func(w http.ResponseWriter, r *http.Request) bool {
		_, reference, ok := strings.Cut(r.URL.Path, "/manifests/")
		if r.Method != http.MethodDelete || !ok || strings.HasPrefix(reference, "sha256:") {
			return false
		}
		w.WriteHeader(status)
		return true
	}
}

// digestDeletes are the DELETE requests the destination received by digest.
func (h *mirrorHarness) digestDeletes() []registrytest.Request {
	return h.destination.Requests(http.MethodDelete, "/manifests/sha256:")
}

// tagHeads are the manifest HEAD requests the destination received for the tag ref, relative
// to it.
func (h *mirrorHarness) tagHeads(ref string) []registrytest.Request {
	repository, tag, _ := strings.Cut(ref, ":")
	return h.destination.Requests(http.MethodHead, "/v2/"+repository+"/manifests/"+tag)
}

// withDeleteByDigest sets cleanup.deleteByDigest on the mirror under test.
func withDeleteByDigest(im *kuikv1alpha1.ImageMirror) {
	if im.Spec.Cleanup == nil {
		im.Spec.Cleanup = &kuikv1alpha1.Cleanup{}
	}
	im.Spec.Cleanup.DeleteByDigest = new(true)
}

// deleteTag deletes the tag ref, relative to the destination, and leaves its manifest.
func (h *mirrorHarness) deleteTag(ref string) {
	GinkgoHelper()
	Expect(kuikregistry.NewClient().DeleteTag(ctx, kuikregistry.Endpoint{
		Reference: h.destination.Host() + "/" + ref, Insecure: true,
	})).To(Succeed())
}

// orphan writes the tags of acme/<name> under the destination of a source the mirror does not
// copy from, as a past copy left them, lists their repository in status.repositories, and
// returns the destination reference of the first tag, relative to the destination.
func (h *mirrorHarness) orphan(name string, tags ...string) string {
	GinkgoHelper()
	repository := "mirror/quay.io/acme/" + name
	image := registrytest.Image()
	for _, tag := range tags {
		h.destination.Push(repository+":"+tag, image)
	}
	h.inventory(h.destination.Host() + "/" + repository)
	return repository + ":" + tags[0]
}

// inventory adds repository to the status.repositories of the mirror under test.
func (h *mirrorHarness) inventory(repository string) {
	GinkgoHelper()
	var im kuikv1alpha1.ImageMirror
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: h.name}, &im)).To(Succeed())
	im.Status.Repositories = append(im.Status.Repositories, repository)
	Expect(k8sClient.Status().Update(ctx, &im)).To(Succeed())
}

// counter returns the value of the counter metric of registry carrying labels, 0 when the
// series does not exist.
func counter(registry *prometheus.Registry, metric string, labels map[string]string) float64 {
	GinkgoHelper()
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() != metric {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue series
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// observations returns how many samples the histogram metric of registry holds, all series
// summed.
func observations(registry *prometheus.Registry, metric string) uint64 {
	GinkgoHelper()
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	var n uint64
	for _, f := range families {
		if f.GetName() == metric {
			for _, m := range f.GetMetric() {
				n += m.GetHistogram().GetSampleCount()
			}
		}
	}
	return n
}

// manyImages runs n images in pods of a selected namespace, spread over ten source
// registries so that their windows open side by side, all on their sources, the same content
// under every reference. Each image has a repository of its own with repositories, else they
// are tags of one repository per source: a first copy into a repository writes the status,
// which n repositories make slow. It returns the pods, the sources, and each image relative to
// its source.
func (h *mirrorHarness) manyImages(n int, repositories bool) ([]*corev1.Pod, []*registrytest.Registry, []string) {
	GinkgoHelper()
	sources := make([]*registrytest.Registry, 10)
	for i := range sources {
		sources[i] = h.source()
	}
	image := registrytest.Image()
	ns := h.namespace()
	var pods []*corev1.Pod
	refs := make([]string, 0, n)
	containers := make([]container, 0, 10)
	for i := range n {
		src := sources[i%len(sources)]
		ref := fmt.Sprintf("acme/app:t%03d", i)
		if repositories {
			ref = fmt.Sprintf("acme/r%03d:v1", i)
		}
		src.Push(ref, image)
		refs = append(refs, ref)
		containers = append(containers, container{name: fmt.Sprintf("c%d", i%10), image: src.Host() + "/" + ref})
		if len(containers) == 10 || i == n-1 {
			pods = append(pods, createPod(ns, unique("pod"), containers...))
			containers = nil
		}
	}
	for _, src := range sources {
		src.Reset()
	}
	return pods, sources, refs
}

// windowsUntil opens windows until done holds, reconciling every ten windows: with hundreds of
// images a reconcile costs more than a window. A window opening while the previous copy or
// check of its host still runs is lost, so the count is not fixed.
func (h *mirrorHarness) windowsUntil(done func() bool) {
	GinkgoHelper()
	Eventually(func() bool {
		h.reconcile()
		for range 10 {
			h.window()
			time.Sleep(5 * time.Millisecond)
		}
		h.reconcile()
		return done()
	}).WithTimeout(3 * time.Minute).WithPolling(time.Millisecond).Should(BeTrue())
}

// withRetention sets the cleanup.retention of the mirror under test.
func withRetention(retention time.Duration) func(*kuikv1alpha1.ImageMirror) {
	return func(im *kuikv1alpha1.ImageMirror) {
		im.Spec.Cleanup = &kuikv1alpha1.Cleanup{Retention: &metav1.Duration{Duration: retention}}
	}
}

// copyAll runs one copy window per image the mirror owes, n in all, each one waiting for the
// previous copy to write its manifest: a window opening while a copy runs is lost.
func (h *mirrorHarness) copyAll(n int) {
	GinkgoHelper()
	start := len(h.destination.Requests(http.MethodPut, "/manifests/"))
	for i := range n {
		h.reconcile()
		h.window()
		Eventually(func() int {
			return len(h.destination.Requests(http.MethodPut, "/manifests/"))
		}).Should(BeNumerically(">", start+i))
	}
	h.reconcile()
}

// checked returns how many manifest HEADs src received for repository:v1.
func checked(src *registrytest.Registry, repository string) func() int {
	return func() int {
		return len(src.Requests(http.MethodHead, "/v2/"+repository+"/manifests/v1"))
	}
}

// withDrift sets the driftPolicy of the mirror under test.
func withDrift(policy kuikv1alpha1.DriftPolicy) func(*kuikv1alpha1.ImageMirror) {
	return func(im *kuikv1alpha1.ImageMirror) { im.Spec.DriftPolicy = policy }
}

// fallBack runs the window of the origin host, which the origin fails to answer, then
// reconciles until the reference waits in the copy queue of alternative.
func (h *mirrorHarness) fallBack(alternative *registrytest.Registry) {
	GinkgoHelper()
	h.reconcile()
	h.window()
	Eventually(func() bool {
		h.reconcile()
		return h.queued(alternative)
	}).WithTimeout(time.Second).Should(BeTrue())
}

// queued reports whether the scheduler holds a copy queue on the host of reg: it then
// exposes the copy pace of that host.
func (h *mirrorHarness) queued(reg *registrytest.Registry) bool {
	scheduling := prometheus.NewRegistry()
	scheduling.MustRegister(h.scheduler.Collector())
	_, ok := gauge(scheduling, "kuik_registry_interval_seconds", map[string]string{labelRegistry: reg.Host(), "operation": "Copy"})
	return ok
}

// scanned reports whether the scheduler exports the Scan interval of the destination host.
func (h *mirrorHarness) scanned() bool {
	scheduling := prometheus.NewRegistry()
	scheduling.MustRegister(h.scheduler.Collector())
	_, ok := gauge(scheduling, "kuik_registry_interval_seconds", map[string]string{labelRegistry: h.destination.Host(), labelOperation: "Scan"})
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
	h.alternativeWithPolicy("", entries...)
}

// alwaysAlternativeTo is alternativeTo with rewritePolicy Always: at admission its
// candidates come before the origin.
func (h *mirrorHarness) alwaysAlternativeTo(entries ...kuikv1alpha1.Alternative) {
	h.alternativeWithPolicy(kuikv1alpha1.RewritePolicyAlways, entries...)
}

func (h *mirrorHarness) alternativeWithPolicy(policy kuikv1alpha1.RewritePolicy, entries ...kuikv1alpha1.Alternative) {
	Expect(k8sClient.Create(ctx, &kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: unique("ia")},
		Spec: kuikv1alpha1.ImageAlternativeSpec{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{testLabel: h.name}},
			RewritePolicy:     policy,
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
