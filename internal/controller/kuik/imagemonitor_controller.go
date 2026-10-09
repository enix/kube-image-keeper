package kuik

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
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
	metrics   *imagemetrics.Exporter

	mu sync.Mutex
	// monitors holds, per monitor, what its status does not persist.
	monitors map[string]*monitorMemory
}

// monitorMemory is what one monitor remembers from its previous report. It lives in the
// process only: an image whose last pod goes away while no reconciler runs is never retained.
type monitorMemory struct {
	// declared are the origins a pod declared at the previous report, each with the digest
	// its most recently started container ran.
	declared map[string]string
	// emptySeen is set when the previous report found no pod while images were tracked.
	emptySeen bool
}

// ImageMonitorOptions are what an ImageMonitorReconciler reports with.
type ImageMonitorOptions struct {
	// Recorder emits the events on the monitors.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
	// Clock dates the retained images.
	Clock clock.Clock
}

// NewImageMonitorReconciler returns a reconciler writing with c.
func NewImageMonitorReconciler(c client.Client, scheme *runtime.Scheme, opts ImageMonitorOptions) (*ImageMonitorReconciler, error) {
	readiness, err := condition.NewReadiness(opts.Recorder, opts.Registerer)
	if err != nil {
		return nil, err
	}
	metrics, err := imagemetrics.New(opts.Registerer)
	if err != nil {
		return nil, err
	}
	return &ImageMonitorReconciler{
		Client:    c,
		Scheme:    scheme,
		clock:     opts.Clock,
		readiness: readiness,
		metrics:   metrics,
		monitors:  map[string]*monitorMemory{},
	}, nil
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemonitors/status,verbs=update;patch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch,roleName=reconciler

// Reconcile writes the status of one ImageMonitor.
func (r *ImageMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var im kuikv1alpha1.ImageMonitor
	if err := r.Get(ctx, req.NamespacedName, &im); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	next := *im.Status.DeepCopy()
	var requeue time.Duration
	s, notReady := monitorScope(&im)
	// An invalid selector selects nothing: the images stay as last reported rather than all
	// leaving for retention.
	if notReady == nil {
		pods, err := selectedPods(ctx, r, s)
		if err != nil {
			return ctrl.Result{}, err
		}
		memory := r.memory(im.Name)
		if len(pods) == 0 && declaresImages(im.Status) && !memory.emptySeen {
			// A cache not synced yet lists no pod: trust an empty list only the second time.
			memory.emptySeen = true
			log.V(1).Info("Found no pod for an ImageMonitor tracking images, waiting before trusting it")
			return ctrl.Result{RequeueAfter: emptyCacheWait}, nil
		}
		memory.emptySeen = false
		requeue = r.track(ctx, &im, &next, memory, pods)
	}

	r.readiness.Set(&im, kindImageMonitor, im.Name, &next.Conditions, im.Generation, notReady)

	if equality.Semantic.DeepEqual(im.Status, next) {
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	im.Status = next
	if err := r.Status().Update(ctx, &im); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("Updated ImageMonitor status")
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// track writes into next the origins pods declare and the images kept for retention, and
// returns when the next retained image expires, zero when none is retained.
func (r *ImageMonitorReconciler) track(ctx context.Context, im *kuikv1alpha1.ImageMonitor, next *kuikv1alpha1.ImageMonitorStatus, memory *monitorMemory, pods []*corev1.Pod) time.Duration {
	log := logf.FromContext(ctx)
	now := r.clock.Now()
	retention := defaultUnusedImageRetention
	if im.Spec.UnusedImageRetention != nil {
		retention = im.Spec.UnusedImageRetention.Duration
	}

	declared := declaredOrigins(pods)
	var retained []kuikv1alpha1.RetainedImage
	for ref, digest := range memory.declared {
		if _, ok := declared[ref]; !ok {
			retained = append(retained, kuikv1alpha1.RetainedImage{Ref: ref, UnusedSince: metav1.NewTime(now).Rfc3339Copy(), Digest: digest})
			log.Info("Retained an image no pod declares any more", "image", ref)
		}
	}
	for _, entry := range im.Status.RetainedImages {
		if _, ok := declared[entry.Ref]; !ok {
			retained = append(retained, entry)
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
	previous := memory.declared
	memory.declared = make(map[string]string, len(declared))
	for ref, d := range declared {
		if d.running {
			counts.Running++
		} else {
			counts.Standby++
		}
		digest := d.digest
		if digest == "" {
			// The container has not reported its image yet: keep the digest last seen.
			digest = previous[ref]
		}
		memory.declared[ref] = digest
	}

	next.RetainedImages = retained
	next.Images = &kuikv1alpha1.MonitorImages{Origin: &kuikv1alpha1.OriginImageCounts{ImageCounts: kuikv1alpha1.ImageCounts{
		Tracked:  counts.Running + counts.Standby + counts.Retained,
		Running:  counts.Running,
		Standby:  counts.Standby,
		Retained: counts.Retained,
	}}}
	r.metrics.SetCounts(kindImageMonitor, im.Name, imagemetrics.ReferenceOrigin, counts)
	return requeue
}

// declaredImage is an origin some selected pod declares.
type declaredImage struct {
	// running is true when a container carries the origin itself.
	running bool
	// digest is what the most recently started of those containers ran, started when.
	digest    string
	startedAt time.Time
}

// declaredOrigins returns the origins the containers of pods declare: the origin of a
// standing kuik rewrite, the live image otherwise. An origin is running when any container
// carries that exact reference, whatever it declares. See docs/v3/status.md, "Attribution".
func declaredOrigins(pods []*corev1.Pod) map[string]declaredImage {
	declared := map[string]declaredImage{}
	live := map[string]declaredImage{}
	for _, pod := range pods {
		statuses := map[string]corev1.ContainerStatus{}
		for _, st := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
			statuses[st.Name] = st
		}
		for _, c := range attribution.Containers(pod) {
			declared[c.Origin] = declaredImage{}
			l := live[c.Image]
			l.running = true
			if digest, startedAt, ok := ranDigest(statuses[c.Name]); ok && (l.digest == "" || startedAt.After(l.startedAt)) {
				l.digest, l.startedAt = digest, startedAt
			}
			live[c.Image] = l
		}
	}
	for origin := range declared {
		declared[origin] = live[origin]
	}
	return declared
}

// ranDigest returns the digest a container status reports its image ran from, and when the
// container started.
func ranDigest(st corev1.ContainerStatus) (string, time.Time, bool) {
	_, digest, found := strings.Cut(st.ImageID, "@")
	if !found {
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
func (r *ImageMonitorReconciler) memory(name string) *monitorMemory {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.monitors[name]
	if !ok {
		m = &monitorMemory{}
		r.monitors[name] = m
	}
	return m
}

func (r *ImageMonitorReconciler) forget(name string) {
	r.mu.Lock()
	delete(r.monitors, name)
	r.mu.Unlock()
	r.metrics.Forget(kindImageMonitor, name)
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
// pod or namespace change after StatusInterval.
func (r *ImageMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.StatusInterval == 0 {
		r.StatusInterval = DefaultStatusInterval
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMonitor{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Pod{}, delayed(r.StatusInterval, r.selecting)).
		Watches(&corev1.Namespace{}, delayed(r.StatusInterval, r.all)).
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
