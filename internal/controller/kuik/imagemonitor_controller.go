package kuik

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
	"github.com/enix/kube-image-keeper/internal/status/capped"
	"github.com/enix/kube-image-keeper/internal/status/condition"
	"github.com/enix/kube-image-keeper/internal/status/imagemetrics"
)

const (
	// kindImageMonitor is the kind the series and the events of a monitor carry.
	kindImageMonitor = "ImageMonitor"
	// defaultUnusedImageRetention applies when the spec leaves unusedImageRetention unset.
	defaultUnusedImageRetention = 168 * time.Hour
	// emptyCacheWait is how long a report that found no pod waits before trusting it.
	emptyCacheWait = time.Second
)

// ImageMonitorReconciler writes the status of the ImageMonitors: the images the pods they
// select run, and the ones kept for unusedImageRetention once no pod declares them.
type ImageMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// StatusInterval delays the report a pod or namespace change asks for. Zero means
	// DefaultStatusInterval.
	StatusInterval time.Duration

	clock     clock.Clock
	readiness *condition.Readiness
	limiter   *capped.Limiter
	metrics   *monitorMetrics
	scheduler *pacing.Scheduler
	registry  *registry.Client
	recorder  events.EventRecorder
	// resolver reads fallbackAuth, podSecrets the pull secrets of the pods declaring an
	// image: the two steps of docs/v3/spec.md, "No auth at all", a check reads with.
	resolver   *auth.Resolver
	podSecrets *auth.Resolver

	// secretReader, namespace and fallbackAuth are what fallbackSecrets reads the Secrets of
	// fallbackAuth again with, once per Secret and per report.
	secretReader client.Reader
	namespace    string
	fallbackAuth atomic.Pointer[[]auth.FallbackAuthEntry]

	mu sync.Mutex
	// monitors holds, per monitor, what its status does not persist.
	monitors map[string]*monitorMemory
	// wakeFn asks for a report of a monitor once a check of it ended. Nil without a manager.
	wakeFn func(name string)
}

// monitorMemory is what one monitor remembers from its previous report. It lives in the
// process only: an image whose last pod goes away while no reconciler runs is never retained.
type monitorMemory struct {
	// declared are the origins a pod declared at the previous report, each with the digest
	// its most recently started container ran.
	declared map[string]string
	// offered are the alternatives each origin was offered at the previous report, kept for an
	// origin while it is retained.
	offered offered
	// emptySeen is set when the previous report found no pod while images were tracked.
	emptySeen bool
	// uid is the UID of the monitor this memory belongs to.
	uid types.UID
	// seeded is set once the verdicts were read back from the status.
	seeded bool
	// persisted is what the status held beyond its capped list when it was read back, kept
	// until every tracked image has a verdict again; nil then.
	persisted *persistedCounts
	// rings are the registry hosts the monitor holds a check ring on.
	rings map[string]bool

	// mu guards what the checks write, from verdicts to upstream.
	mu sync.Mutex
	// verdicts are the outcome of the last check of each tracked image.
	verdicts map[string]verdict
	// declaring are the pods declaring each image, whose pull secrets a check tries.
	declaring map[string][]*corev1.Pod
	// secretErrs are the fallbackAuth Secrets the last check of an image could not read.
	secretErrs map[string]error
	// anonymous records, per image, whether its last check sent no credential.
	anonymous map[string]bool
	// upstream is, per image, the digest its last successful check read: a failed check keeps
	// the one before.
	upstream map[string]string

	// drifts are the tags reported drifted.
	drifts map[string]driftState
	// undigested are the tags logged once as running without a registry digest.
	undigested map[string]bool
}

// ImageMonitorOptions are what an ImageMonitorReconciler reports with.
type ImageMonitorOptions struct {
	// Recorder emits the events on the monitors.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
	// Clock dates the retained images.
	Clock clock.Clock
	// Scheduler paces the checks against the registries. Nil checks nothing.
	Scheduler *pacing.Scheduler
	// Registry reads the tracked images.
	Registry *registry.Client
	// APIReader reads the Secrets the checks use, uncached.
	APIReader client.Reader
	// ClusterResourceNamespace is where a fallbackAuth secretRef resolves.
	ClusterResourceNamespace string
	// Config is the global config as loaded at start-up; SetConfig hands over a reload.
	Config *config.Config
	// ListCapacity caps the anomaly lists of the status. Zero means capped.Capacity.
	ListCapacity int
}

// NewImageMonitorReconciler returns a reconciler writing with c.
func NewImageMonitorReconciler(c client.Client, scheme *runtime.Scheme, opts ImageMonitorOptions) (*ImageMonitorReconciler, error) {
	readiness, err := condition.NewReadiness(opts.Recorder, opts.Registerer)
	if err != nil {
		return nil, err
	}
	metrics, err := newMonitorMetrics(opts.Registerer)
	if err != nil {
		return nil, err
	}
	var capacity []capped.Option
	if opts.ListCapacity > 0 {
		capacity = append(capacity, capped.WithCapacity(opts.ListCapacity))
	}
	limiter, err := capped.NewLimiter(opts.Registerer, capacity...)
	if err != nil {
		return nil, err
	}
	r := &ImageMonitorReconciler{
		Client:    c,
		Scheme:    scheme,
		clock:     opts.Clock,
		readiness: readiness,
		limiter:   limiter,
		metrics:   metrics,
		scheduler: opts.Scheduler,
		registry:  opts.Registry,
		recorder:  opts.Recorder,
		resolver:  auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeReconciler),
		// Webhook mode skips fallbackAuth: it reads the pull secrets of a pod and nothing else,
		// since an origin has no entry auth.
		podSecrets: auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeWebhook),
		monitors:   map[string]*monitorMemory{},
	}
	r.secretReader, r.namespace = opts.APIReader, opts.ClusterResourceNamespace
	r.fallbackAuth.Store(&[]auth.FallbackAuthEntry{})
	if opts.Config != nil {
		if err := r.resolver.SetFallbackAuth(opts.Config.FallbackAuth); err != nil {
			return nil, err
		}
		r.fallbackAuth.Store(&opts.Config.FallbackAuth)
	}
	return r, nil
}

// SetConfig applies a reloaded global config.
func (r *ImageMonitorReconciler) SetConfig(cfg *config.Config) {
	if err := r.resolver.SetFallbackAuth(cfg.FallbackAuth); err != nil {
		logf.Log.Info("Kept the previous fallbackAuth of the ImageMonitor reconciler", "error", err.Error())
		return
	}
	r.fallbackAuth.Store(&cfg.FallbackAuth)
}

// wake asks for a report of the monitor name.
func (r *ImageMonitorReconciler) wake(name string) {
	if r.wakeFn != nil {
		r.wakeFn(name)
	}
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/status,verbs=update;patch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives,verbs=get;list;watch,roleName=reconciler

// A check reads the fallbackAuth Secrets in the install namespace (a Role: the chart puts it in
// the release namespace, kuik-system is a placeholder), and the imagePullSecrets of the pods
// declaring the image cluster-wide, bound according to secretAccess.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// Reconcile writes the status of one ImageMonitor.
func (r *ImageMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var im kuikv1alpha1.ImageMonitor
	if err := r.Get(ctx, req.NamespacedName, &im); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(ctx, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	next := *im.Status.DeepCopy()
	var requeue time.Duration
	// remember records what this report saw, once the status it wrote holds it: a failed
	// write leaves the memory as it was, so the next attempt retains the same images.
	remember := func() {}
	if previous := r.monitor(im.Name); previous != nil && previous.uid != im.UID {
		// Deleted and created again under the same name while no reconcile saw it gone: the
		// new monitor starts from its own status, never from the memory of the old one.
		r.forget(ctx, im.Name)
	}
	memory := r.memory(im.Name, im.UID)
	memory.seed(&im.Status)
	s, notReady := monitorScope(&im)
	// selected is set when the selectors parse, so that the rings follow what they select.
	selected := notReady == nil
	// An invalid selector selects nothing: the images stay as last reported rather than all
	// leaving for retention.
	if notReady == nil {
		pods, err := selectedPods(ctx, r, s)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(pods) == 0 && declaresImages(im.Status) && !memory.emptySeen {
			// A cache not synced yet lists no pod: trust an empty list only the second time.
			memory.emptySeen = true
			log.V(1).Info("Found no pod for an ImageMonitor tracking images, waiting before trusting it")
			return ctrl.Result{RequeueAfter: emptyCacheWait}, nil
		}
		memory.emptySeen = false
		var offers *alternativeOffers
		if im.Spec.MonitorsAlternatives() {
			if offers, err = r.alternativeOffers(ctx); err != nil {
				return ctrl.Result{}, err
			}
		}
		var seen reported
		requeue, seen, notReady = r.track(ctx, &im, &next, memory, pods, offers)
		remember = func() { memory.declared, memory.offered = seen.declared, seen.offered }
	}

	// A process that selects nothing yet holds no ring: the persisted cursors stay, for the
	// rings to resume from once the selectors parse again.
	if r.scheduler != nil && (selected || len(memory.rings) > 0) {
		next.Checks = r.checks(im.Name)
	}
	r.readiness.Set(&im, kindImageMonitor, im.Name, &next.Conditions, im.Generation, notReady)

	if equality.Semantic.DeepEqual(im.Status, next) {
		remember()
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	im.Status = next
	if err := r.Status().Update(ctx, &im); err != nil {
		return ctrl.Result{}, err
	}
	remember()
	log.V(1).Info("Updated ImageMonitor status")
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// track writes into next the origins pods declare, the images kept for retention and, when
// offers is set, the alternatives offered for them. It returns when the next retained image
// expires, zero when none is retained, and what the report saw, for the memory once next is
// written.
func (r *ImageMonitorReconciler) track(ctx context.Context, im *kuikv1alpha1.ImageMonitor, next *kuikv1alpha1.ImageMonitorStatus, memory *monitorMemory, pods []*corev1.Pod, offers *alternativeOffers) (time.Duration, reported, *condition.NotReady) {
	log := logf.FromContext(ctx)
	now := r.clock.Now()
	retention := defaultUnusedImageRetention
	if im.Spec.UnusedImageRetention != nil {
		retention = im.Spec.UnusedImageRetention.Duration
	}

	declared := declaredOrigins(pods)
	var retained []kuikv1alpha1.RetainedImage
	already := map[string]bool{}
	for _, entry := range im.Status.RetainedImages {
		if _, ok := declared[entry.Ref]; !ok {
			retained = append(retained, entry)
			already[entry.Ref] = true
		}
	}
	for ref, digest := range memory.declared {
		if _, ok := declared[ref]; !ok && !already[ref] {
			retained = append(retained, kuikv1alpha1.RetainedImage{Ref: ref, UnusedSince: metav1.NewTime(now).Rfc3339Copy(), Digest: digest})
			log.Info("Retained an image no pod declares any more", "image", ref)
		}
	}
	var requeue time.Duration
	retained = slices.DeleteFunc(retained, func(e kuikv1alpha1.RetainedImage) bool {
		left := e.UnusedSince.Add(retention).Sub(now)
		if left <= 0 {
			log.Info("Dropped a retained image whose retention elapsed", "image", e.Ref)
			return true
		}
		if requeue == 0 || left < requeue {
			requeue = left
		}
		return false
	})
	slices.SortFunc(retained, func(a, b kuikv1alpha1.RetainedImage) int { return cmp.Compare(a.Ref, b.Ref) })

	counts := imagemetrics.Counts{Retained: int32(len(retained))}
	digests := make(map[string]string, len(declared))
	tracked := make([]string, 0, len(declared)+len(retained))
	for ref, d := range declared {
		if d.running {
			counts.Running++
		} else {
			counts.Standby++
		}
		digest := d.digest
		if digest == "" {
			// The container has not reported its image yet: keep the digest last seen.
			digest = memory.declared[ref]
		}
		digests[ref] = digest
		tracked = append(tracked, ref)
	}
	for _, e := range retained {
		tracked = append(tracked, e.Ref)
	}

	declaring := make(map[string][]*corev1.Pod, len(declared))
	for ref, d := range declared {
		declaring[ref] = d.pods
	}
	memory.setDeclaring(declaring)
	var alternatives map[string]*monitoredAlternative
	var offeredTo offered
	if offers != nil {
		alternatives, offeredTo = offers.of(pods, retained, memory.offered)
	}
	// A reference both an origin and an alternative sits once in its ring, and is checked once
	// for both.
	ringed := slices.Clone(tracked)
	origins := make(map[string]bool, len(tracked))
	for _, ref := range tracked {
		origins[ref] = true
	}
	for _, ref := range slices.Sorted(maps.Keys(alternatives)) {
		if !origins[ref] {
			ringed = append(ringed, ref)
		}
	}
	r.turnRings(ctx, im, memory, ringed)
	// The counts come from the verdicts in memory, never from the list that samples them.
	all, upstream := memory.verdictsOf(ringed)
	verdicts := make(map[string]verdict, len(tracked))
	for _, ref := range tracked {
		if v, ok := all[ref]; ok {
			verdicts[ref] = v
		}
	}
	r.announceDenied(im, memory, verdicts)
	unavailable := make([]kuikv1alpha1.UnavailableImage, 0, len(verdicts))
	failing := map[string]kuikv1alpha1.CheckFailureReason{}
	for ref, v := range verdicts {
		if v.reason == "" {
			counts.Available++
			continue
		}
		counts.Unavailable++
		failing[ref] = v.reason
		unavailable = append(unavailable, kuikv1alpha1.UnavailableImage{
			Ref: ref, Reason: v.reason, Since: metav1.NewTime(v.since), Pods: int32(len(declared[ref].pods)),
		})
	}
	// unavailableImages is a sample, the oldest entries first; retainedImages is an inventory,
	// never capped. See docs/v3/status.md, "Bounded lists".
	pass := r.limiter.Begin(kindImageMonitor, im.Name)
	unavailable = capped.Cap(pass, listUnavailableImages, unavailable,
		func(e kuikv1alpha1.UnavailableImage) metav1.Time { return e.Since },
		func(e kuikv1alpha1.UnavailableImage) string { return e.Ref })
	// The drift count and series come from memory, never from the list that samples them.
	drifted := r.drift(ctx, im, memory, declared, upstream, now)
	driftedCount := int32(len(drifted))
	listed := capped.Cap(pass, listDriftedImages, drifted,
		func(e kuikv1alpha1.MonitorDriftedImage) metav1.Time { return e.Since },
		func(e kuikv1alpha1.MonitorDriftedImage) string { return e.Ref })
	// A restart reads back only the capped lists: until every image a ring can check has a
	// verdict again, the images they left out may still fail or drift, so the report never goes
	// below what the status persisted, within what the images with no verdict yet can account
	// for. A reference that does not parse is on no ring and never gets a verdict: it holds
	// nothing.
	checked := checkable(tracked)
	if !im.Spec.DetectsDrift() {
		// No drift is reported: what the status persisted of it is never held, nor once
		// detection is turned back on.
		memory.forgetHeldDrift()
	}
	// A tag the status listed as drifted is decided already: only the tags whose drift cannot
	// be told yet may stand for the ones the list left out.
	pending := undecided(declared, upstream)
	if held, ok := memory.held(checked, pending); ok {
		unknown := int32(max(len(checked)-len(verdicts), 0))
		counts.Unavailable = max(counts.Unavailable, min(held.unavailable, counts.Unavailable+unknown))
		pass.Hold(listUnavailableImages, min(held.truncated, max(counts.Unavailable-int32(len(unavailable)), 0)))
		driftedCount = max(driftedCount, min(held.drifted, driftedCount+pending))
		pass.Hold(listDriftedImages, min(held.driftTruncated, pending, max(driftedCount-int32(len(listed)), 0)))
	}

	next.RetainedImages = retained
	next.UnavailableImages = unavailable
	if len(unavailable) == 0 {
		next.UnavailableImages = nil
	}
	next.DriftedImages = listed
	if len(listed) == 0 {
		next.DriftedImages = nil
	}
	next.Images = &kuikv1alpha1.MonitorImages{Origin: &kuikv1alpha1.OriginImageCounts{ImageCounts: kuikv1alpha1.ImageCounts{
		Tracked:     counts.Running + counts.Standby + counts.Retained,
		Running:     counts.Running,
		Standby:     counts.Standby,
		Retained:    counts.Retained,
		Available:   counts.Available,
		Unavailable: counts.Unavailable,
	}, Drifted: driftedCount}}
	if offers != nil {
		next.Images.Alternatives = alternativesStatus(alternativeCounts(alternatives))
	}
	condition.SetAnomaly(&next.Conditions, kuikv1alpha1.ConditionImagesUnavailable, counts.Unavailable > 0,
		kuikv1alpha1.ReasonChecksFailed, fmt.Sprintf("%d images failing their check", counts.Unavailable), im.Generation)
	condition.SetAnomaly(&next.Conditions, kuikv1alpha1.ConditionImagesDrifted, driftedCount > 0,
		kuikv1alpha1.ReasonUpstreamDigestMoved, fmt.Sprintf("%d tags whose upstream digest moved", driftedCount), im.Generation)
	next.Truncated = pass.End(&next.Conditions, im.Generation)
	r.metrics.report(im.Name, counts, failing, refsOf(drifted), skewOf(drifted))
	return requeue, reported{declared: digests, offered: offeredTo}, r.fallbackSecrets(ctx, memory)
}

// reported is what one report saw, for the memory once its status is written.
type reported struct {
	// declared are the origins a pod declares, with their digests.
	declared map[string]string
	// offered are the alternatives each tracked origin was offered.
	offered offered
}

// declaredImage is an origin some selected pod declares.
type declaredImage struct {
	// running is true when a container carries the origin itself.
	running bool
	// pods are the pods declaring it, by namespace and name.
	pods []*corev1.Pod
	// digest is what the most recently started of those containers ran, started when.
	digest    string
	startedAt time.Time
	// runningDigests counts, per digest, the pods with a container carrying the reference
	// itself that runs it. A container whose image is not pulled yet runs none.
	runningDigests map[string]int32
	// undigested is a pod with a container carrying the reference itself whose imageID holds
	// no registry digest: an image the node had before any pull (a preloaded, `kind load` or
	// `ctr import` image) reports a bare config digest. Nil when there is none.
	undigested *corev1.Pod
}

// declaredOrigins returns the origins the containers of pods declare: the origin of a
// standing kuik rewrite, the live image otherwise. An origin is running when any container
// carries that exact reference, whatever it declares. See docs/v3/status.md, "Attribution".
func declaredOrigins(pods []*corev1.Pod) map[string]declaredImage {
	declared := map[string]declaredImage{}
	live := map[string]declaredImage{}
	declaring := map[string][]*corev1.Pod{}
	for _, pod := range pods {
		statuses := map[string]corev1.ContainerStatus{}
		for _, st := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
			statuses[st.Name] = st
		}
		origins := map[string]bool{}
		// runs are the digests this pod runs each live image at, so that a pod counts once per
		// digest however many of its containers run it.
		runs := map[string]map[string]bool{}
		for _, c := range attribution.Containers(pod) {
			declared[c.Origin] = declaredImage{}
			if !origins[c.Origin] {
				origins[c.Origin] = true
				declaring[c.Origin] = append(declaring[c.Origin], pod)
			}
			l := live[c.Image]
			l.running = true
			digest, startedAt, ok := ranDigest(statuses[c.Name])
			if ok && (l.digest == "" || startedAt.After(l.startedAt)) {
				l.digest, l.startedAt = digest, startedAt
			}
			if !ok && statuses[c.Name].ImageID != "" && l.undigested == nil {
				l.undigested = pod
			}
			if ok {
				if runs[c.Image] == nil {
					runs[c.Image] = map[string]bool{}
				}
				runs[c.Image][digest] = true
			}
			live[c.Image] = l
		}
		for image, digests := range runs {
			l := live[image]
			if l.runningDigests == nil {
				l.runningDigests = map[string]int32{}
			}
			for digest := range digests {
				l.runningDigests[digest]++
			}
			live[image] = l
		}
	}
	for origin := range declared {
		d := live[origin]
		d.pods = declaring[origin]
		slices.SortFunc(d.pods, func(a, b *corev1.Pod) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})
		declared[origin] = d
	}
	return declared
}

// ranDigest returns the digest a container status reports its image ran from, and when the
// container started.
func ranDigest(st corev1.ContainerStatus) (string, time.Time, bool) {
	_, digest, found := strings.Cut(st.ImageID, "@")
	if !found || digest == "" {
		return "", time.Time{}, false
	}
	switch {
	case st.State.Running != nil:
		return digest, st.State.Running.StartedAt.Time, true
	case st.State.Terminated != nil:
		return digest, st.State.Terminated.StartedAt.Time, true
	}
	return digest, time.Time{}, true
}

// declaresImages reports whether status counts an image some pod declares.
func declaresImages(status kuikv1alpha1.ImageMonitorStatus) bool {
	o := status.Images
	return o != nil && o.Origin != nil && o.Origin.Running+o.Origin.Standby > 0
}

// memory returns what the monitor name remembers, creating it on first use.
func (r *ImageMonitorReconciler) memory(name string, uid types.UID) *monitorMemory {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.monitors[name]
	if !ok {
		m = &monitorMemory{
			uid: uid, rings: map[string]bool{}, verdicts: map[string]verdict{}, secretErrs: map[string]error{},
			anonymous: map[string]bool{}, upstream: map[string]string{}, drifts: map[string]driftState{}, undigested: map[string]bool{},
		}
		r.monitors[name] = m
	}
	return m
}

// forget drops what the deleted monitor name held: its rings, its memory and its series.
func (r *ImageMonitorReconciler) forget(ctx context.Context, name string) {
	// The rings go first: a window opening meanwhile still finds the memory, and one opening
	// after it finds no ring.
	if memory := r.monitor(name); memory != nil {
		r.dropRings(ctx, name, memory, slices.Collect(maps.Keys(memory.rings)))
	}
	r.mu.Lock()
	delete(r.monitors, name)
	r.mu.Unlock()
	r.metrics.forget(name)
	r.limiter.Forget(kindImageMonitor, name)
	r.readiness.Forget(kindImageMonitor, name)
}

// monitorScope parses the selectors of im, and reports InvalidConfig when one does not parse.
func monitorScope(im *kuikv1alpha1.ImageMonitor) (scope, *condition.NotReady) {
	pods, err := selector(im.Spec.PodSelector)
	if err != nil {
		return scope{}, invalidConfig("podSelector", err)
	}
	namespaces, err := selector(im.Spec.NamespaceSelector)
	if err != nil {
		return scope{}, invalidConfig("namespaceSelector", err)
	}
	return scope{pods: pods, namespaces: namespaces}, nil
}

// SetupWithManager sets up the controller with the Manager: a spec change reports at once, a
// pod, namespace or ImageAlternative change and an ended check after StatusInterval.
func (r *ImageMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.StatusInterval == 0 {
		r.StatusInterval = DefaultStatusInterval
	}
	wakes := make(chan event.TypedGenericEvent[*kuikv1alpha1.ImageMonitor], 1024)
	r.wakeFn = func(name string) {
		select {
		case wakes <- event.TypedGenericEvent[*kuikv1alpha1.ImageMonitor]{Object: &kuikv1alpha1.ImageMonitor{ObjectMeta: metav1.ObjectMeta{Name: name}}}:
		default:
			// A full channel already holds reports enough to pick this check up.
		}
	}
	// The checks of a ring end one window apart: delaying their report coalesces them into
	// one status write per StatusInterval.
	checked := handler.TypedFuncs[*kuikv1alpha1.ImageMonitor, reconcile.Request]{
		GenericFunc: func(_ context.Context, e event.TypedGenericEvent[*kuikv1alpha1.ImageMonitor], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			q.AddAfter(reconcile.Request{NamespacedName: types.NamespacedName{Name: e.Object.Name}}, r.StatusInterval)
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMonitor{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Pod{}, delayed(r.StatusInterval, r.selecting)).
		Watches(&corev1.Namespace{}, delayed(r.StatusInterval, r.all)).
		// Its status writes leave what an ImageAlternative offers as it was.
		Watches(&kuikv1alpha1.ImageAlternative{}, delayed(r.StatusInterval, r.watchingAlternatives),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(source.Channel(wakes, checked)).
		Named("kuik-imagemonitor").
		Complete(r)
}

// selecting maps a pod to the ImageMonitors that select it.
func (r *ImageMonitorReconciler) selecting(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Namespace}, &ns); err != nil {
		// Without the namespace labels, every monitor may select the pod.
		return r.all(ctx, obj)
	}
	var list kuikv1alpha1.ImageMonitorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for i := range list.Items {
		if s, _ := monitorScope(&list.Items[i]); s.selects(pod, ns.Labels) {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
	}
	return requests
}

// all maps any object to every ImageMonitor.
func (r *ImageMonitorReconciler) all(ctx context.Context, _ client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageMonitorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, im := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: im.Name}})
	}
	return requests
}
