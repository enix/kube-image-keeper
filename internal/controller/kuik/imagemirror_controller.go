package kuik

import (
	"context"
	"errors"
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/mirrorpath/plan"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/pacing"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/capped"
	"github.com/enix/kube-image-keeper/internal/status/condition"
	"github.com/enix/kube-image-keeper/internal/status/routingstatus"
)

// ImageMirrorReconciler copies the images of the pods each ImageMirror selects to its
// destination, keeps the destination in step with them, and writes the mirror's status: the
// copy side, then the routing side an ImageAlternative also reports.
type ImageMirrorReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// StatusInterval delays the report a pod or namespace change asks for. Zero means
	// DefaultStatusInterval.
	StatusInterval time.Duration

	namespace string
	// resolver reads the Secrets the mirror names; sourceResolver also the pod pull secrets
	// and fallbackAuth, to read a source as the background loops do.
	resolver       *auth.Resolver
	sourceResolver *auth.Resolver
	recorder       events.EventRecorder
	tracker        *routingstatus.Tracker
	limiter        *capped.Limiter
	readiness      *condition.Readiness
	scheduler      *pacing.Scheduler
	registry       *registry.Client
	clock          clock.Clock
	config         atomic.Pointer[config.Config]
	// elected is set once the lease is held; no status is written before it.
	elected atomic.Bool

	metrics *mirrorMetrics

	statesMu sync.Mutex
	states   map[string]*mirrorState
	// wakeFn asks for a reconcile of a mirror once a copy or a check of it ended. Nil without
	// a manager.
	wakeFn func(name string)
}

// conflictWait is how long a reconcile whose status write lost a race waits before trying
// again.
const conflictWait = 100 * time.Millisecond

// wake asks for a reconcile of the mirror name.
func (r *ImageMirrorReconciler) wake(name string) {
	if r.wakeFn != nil {
		r.wakeFn(name)
	}
}

// ImageMirrorOptions are what an ImageMirrorReconciler reads, copies and reports with.
type ImageMirrorOptions struct {
	// APIReader reads the Secrets a secretRef names, uncached.
	APIReader client.Reader
	// ClusterResourceNamespace is where a secretRef resolves.
	ClusterResourceNamespace string
	// Recorder emits the events, on the mirrors and on the pods.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
	// Scheduler paces the copies and the drift checks against the source registries.
	Scheduler *pacing.Scheduler
	// Registry reads the sources and writes the destination.
	Registry *registry.Client
	// Config is the global config as loaded at start-up; SetConfig hands over a reload.
	Config *config.Config
	// Clock dates the destination passes and the status entries.
	Clock clock.Clock
	// ListCapacity caps the anomaly lists of the status. Zero means capped.Capacity.
	ListCapacity int
}

// NewImageMirrorReconciler returns a reconciler writing with c.
func NewImageMirrorReconciler(c client.Client, scheme *runtime.Scheme, opts ImageMirrorOptions) (*ImageMirrorReconciler, error) {
	tracker, err := routingstatus.NewTracker(opts.Recorder, opts.Registerer)
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
	readiness, err := condition.NewReadiness(opts.Recorder, opts.Registerer)
	if err != nil {
		return nil, err
	}
	metrics, err := newMirrorMetrics(opts.Registerer)
	if err != nil {
		return nil, err
	}
	r := &ImageMirrorReconciler{
		Client:    c,
		Scheme:    scheme,
		namespace: opts.ClusterResourceNamespace,
		// Webhook mode skips fallbackAuth: Ready answers for the Secrets the mirror itself
		// names, never for the global config.
		resolver:       auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeWebhook),
		sourceResolver: auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeReconciler),
		recorder:       opts.Recorder,
		tracker:        tracker,
		limiter:        limiter,
		readiness:      readiness,
		scheduler:      opts.Scheduler,
		registry:       opts.Registry,
		clock:          opts.Clock,
		metrics:        metrics,
		states:         map[string]*mirrorState{},
	}
	if err := r.sourceResolver.SetFallbackAuth(opts.Config.FallbackAuth); err != nil {
		return nil, err
	}
	r.config.Store(opts.Config)
	return r, nil
}

// SetConfig applies a reloaded global config.
func (r *ImageMirrorReconciler) SetConfig(cfg *config.Config) {
	r.config.Store(cfg)
	if err := r.sourceResolver.SetFallbackAuth(cfg.FallbackAuth); err != nil {
		logf.Log.Info("Kept the previous fallbackAuth of the ImageMirror reconciler", "error", err.Error())
	}
}

// Elected records when the lease was acquired: pod events go only to pods created since.
func (r *ImageMirrorReconciler) Elected(at time.Time) {
	r.tracker.Elected(at)
	r.elected.Store(true)
}

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagemirrors/status,verbs=update;patch,roleName=reconciler

// The reads the reconciler shares across its loops, declared once here, on the loop that
// talks to registries. Secrets are read in the install namespace (a Role: the chart puts it
// in the release namespace, kuik-system is a placeholder).
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=reconciler

// Cluster-wide Secret read, for the imagePullSecrets of the pods a check concerns. The chart
// binds it according to secretAccess, to the webhook and the reconciler, never to the syncer.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// Reconcile copies what one ImageMirror owes its destination and writes its status.
func (r *ImageMirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// A report before the lease time is known would persist a staleRewrites entry without its
	// RewriteStale, which the next leader would then never announce.
	if !r.elected.Load() {
		return ctrl.Result{RequeueAfter: electionWait}, nil
	}
	resource := routing.Resource{Kind: routing.KindImageMirror, Name: req.Name}

	var im kuikv1alpha1.ImageMirror
	if err := r.Get(ctx, req.NamespacedName, &im); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(resource)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !im.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &im, resource)
	}
	if controllerutil.AddFinalizer(&im, mirrorFinalizer) {
		if err := r.Update(ctx, &im); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{RequeueAfter: conflictWait}, nil
			}
			return ctrl.Result{}, err
		}
		logf.FromContext(ctx).Info("Added the finalizer of the ImageMirror")
	}

	scope, notReady := mirrorScope(&im)
	// A mirror copies unless it cannot: selectors that do not parse select nothing, and a
	// manage credential that cannot be read writes nothing.
	blocked := notReady != nil
	if notReady == nil {
		var err error
		if notReady, blocked, err = r.checkSecrets(ctx, &im); err != nil {
			return ctrl.Result{}, err
		}
	}
	pods, err := selectedPods(ctx, r, scope)
	if err != nil {
		return ctrl.Result{}, err
	}

	st := r.state(im.Name)
	st.seed(&im)
	clusterID := r.config.Load().ClusterID
	cleanup := im.Spec.Cleanup.IsEnabled()
	mirror := plan.Mirror{Path: im.Spec.Destination.Path, ExcludeImages: im.Spec.ExcludeImages, CleanupEnabled: cleanup}
	live := plan.Desired(plan.Mirror{Path: mirror.Path, ExcludeImages: mirror.ExcludeImages}, pods, nil)
	now := r.clock.Now()
	// A reference losing its last pod is held for its retention, which the sweep of the next
	// pass would only notice an interval later. Live is remembered once the status holding the
	// release is written: a lost write releases it again.
	released := st.release(live)
	var pending []kuikv1alpha1.PendingDeletion
	if cleanup {
		pending = plan.Release(clusterID, mirror.Path, released, live, im.Status.PendingDeletion, now)
	}
	// The live references plus the retained ones: reading the pods once is what a mirror
	// selecting thousands of containers can afford per reconcile.
	desired := union(live, plan.Desired(mirror, nil, pending))
	repositories := im.Status.Repositories
	var (
		selfChecked *metav1.Time
		nextPass    time.Duration
	)
	if !blocked {
		r.scheduler.SetDestinationScan(pacing.Owner{Kind: resource.Kind, Name: resource.Name}, destinationHost(im.Spec.Destination.Path))
		var due bool
		if due, nextPass = r.passDue(st, now); due {
			at, err := r.selfCheck(ctx, &im, st, desired)
			if err == nil && cleanup {
				// An interrupted sweep keeps pendingDeletion as it was.
				var (
					swept  []kuikv1alpha1.PendingDeletion
					retire []string
				)
				if swept, retire, err = r.sweep(ctx, &im, st, live, pending, at); err == nil {
					pending = swept
					repositories = slices.DeleteFunc(slices.Clone(repositories), func(repo string) bool { return slices.Contains(retire, repo) })
				}
			}
			switch {
			case err == nil:
				selfChecked = &metav1.Time{Time: at}
				r.metrics.selfChecked.WithLabelValues(resource.Kind, resource.Name).Set(float64(at.Unix()))
			case errors.Is(err, errPassInterrupted):
				logf.FromContext(ctx).V(1).Info("Interrupted ImageMirror destination pass", "error", err.Error())
			default:
				return ctrl.Result{}, err
			}
		}
		sources, err := r.copySources(ctx, desired, pods)
		if err != nil {
			return ctrl.Result{}, err
		}
		inventoried := func(ref imagepath.Reference) bool {
			return slices.Contains(im.Status.Repositories, destinationRepository(&im, ref, clusterID))
		}
		st.mu.Lock()
		st.declaring = map[string]*corev1.Pod{}
		for ref, copySource := range sources {
			st.declaring[ref] = copySource.pod
		}
		st.mu.Unlock()
		r.turnRings(&im, st, desired)
		r.owe(&im, st, st.owed(desired, inventoried), sources)
	}

	// Under rewritePolicy None the mirror never routes, so it has no routing side at all.
	var routingStatus kuikv1alpha1.RoutingStatus
	if im.Spec.RewritePolicy != kuikv1alpha1.RewritePolicyNone {
		routingStatus = r.tracker.Report(routingstatus.Input{
			Resource: resource,
			Pods:     pods,
			Previous: im.Status.RoutingStatus,
			Now:      metav1.NewTime(r.clock.Now()),
		})
	}

	next := *im.Status.DeepCopy()
	next.RoutingStatus = routingStatus
	routingstatus.SetConditions(&next.Conditions, resource.Kind, routingStatus, im.Generation)
	pass := r.limiter.Begin(resource.Kind, resource.Name)
	routingstatus.Cap(pass, &next.RoutingStatus)
	next.DriftedImages = capped.Cap(pass, "driftedImages", r.reportDrift(&im, st),
		func(e kuikv1alpha1.MirrorDriftedImage) metav1.Time { return e.Since },
		func(e kuikv1alpha1.MirrorDriftedImage) string { return e.Ref })
	next.Checks = r.checks(&im)
	next.FailedImageCopies = r.reportFailures(&im, st, desired)
	next.FailedImageCopies = capped.Cap(pass, "failedImageCopies", next.FailedImageCopies,
		func(e kuikv1alpha1.FailedImageCopy) metav1.Time { return e.Since },
		func(e kuikv1alpha1.FailedImageCopy) string { return e.Ref })
	next.PendingDeletion = pending
	next.Repositories = repositories
	if selfChecked != nil {
		next.SelfChecked = selfChecked
	}
	present, copied := st.verdicts()
	// The series carry every failing and drifted image, where the status keeps a sample.
	failed, drifted := st.anomalies()
	reasons, driftedRefs := counted(failed, drifted)
	counts := plan.Counts(plan.Observed{
		ClusterID:   clusterID,
		Path:        mirror.Path,
		Live:        live,
		Images:      liveImages(pods),
		SelfChecked: present,
		Copied:      copied,
		Drifted:     driftedRefs,
		Failed:      reasons,
		Status:      next,
	})
	next.Images = &kuikv1alpha1.MirrorImages{Copy: &counts}
	condition.SetAnomaly(&next.Conditions, kuikv1alpha1.ConditionDestinationOutOfSync, counts.Unavailable > 0,
		kuikv1alpha1.ReasonMissingImages, fmt.Sprintf("%d images not copied yet", counts.Unavailable), im.Generation)
	r.metrics.report(resource.Kind, resource.Name, counts, failed, drifted)
	next.Truncated = pass.End(&next.Conditions, im.Generation)
	if notReady == nil && cleanup && st.refusesDeletion() {
		notReady = &condition.NotReady{
			Reason:  kuikv1alpha1.ReasonRegistryDeleteUnsupported,
			Message: "the destination refuses tag deletion: cleanup cannot make progress",
		}
	}
	r.readiness.Set(&im, resource.Kind, resource.Name, &next.Conditions, im.Generation, notReady)

	// The next destination pass is due in nextPass, whatever the pods do meanwhile.
	result := ctrl.Result{RequeueAfter: nextPass}
	if equality.Semantic.DeepEqual(im.Status, next) {
		st.remember(live)
		return result, nil
	}
	im.Status = next
	if err := r.Status().Update(ctx, &im); err != nil {
		// A copy records its repository in the same status: the next reconcile reads it.
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: conflictWait}, nil
		}
		return ctrl.Result{}, err
	}
	st.remember(live)
	logf.FromContext(ctx).V(1).Info("Updated ImageMirror status")
	return result, nil
}

func (r *ImageMirrorReconciler) forget(resource routing.Resource) {
	r.tracker.Forget(resource)
	r.limiter.Forget(resource.Kind, resource.Name)
	r.readiness.Forget(resource.Kind, resource.Name)
	r.metrics.forget(resource.Kind, resource.Name)
	r.drop(resource.Name)
}

// counted indexes the reason of each failing copy, and the drifted references.
func counted(failed map[string]copyFailure, drifted []string) (map[string]kuikv1alpha1.CopyFailureReason, map[string]bool) {
	reasons := make(map[string]kuikv1alpha1.CopyFailureReason, len(failed))
	for ref, failure := range failed {
		reasons[ref] = failure.reason
	}
	driftedRefs := make(map[string]bool, len(drifted))
	for _, ref := range drifted {
		driftedRefs[ref] = true
	}
	return reasons, driftedRefs
}

// union returns the references of a and b, once each, sorted as plan.Desired sorts them.
func union(a, b []imagepath.Reference) []imagepath.Reference {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]imagepath.Reference, 0, len(a)+len(b))
	for _, ref := range slices.Concat(a, b) {
		if key := ref.String(); !seen[key] {
			seen[key] = true
			out = append(out, ref)
		}
	}
	slices.SortFunc(out, func(x, y imagepath.Reference) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// mirrorScope parses the selectors of im, and reports InvalidConfig when one does not parse.
func mirrorScope(im *kuikv1alpha1.ImageMirror) (scope, *condition.NotReady) {
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

// checkSecrets reads the Secret of the manage and pull secretRefs of im, and reports
// SecretNotFound or SecretMalformed for the first one that is missing or not a
// dockerconfigjson. A credential left out is anonymous and reads nothing.
// It also reports whether copying is impossible: only the manage credential writes the
// destination, so a broken pull credential leaves Ready False and the copies running.
func (r *ImageMirrorReconciler) checkSecrets(ctx context.Context, im *kuikv1alpha1.ImageMirror) (*condition.NotReady, bool, error) {
	for _, credentials := range []*kuikv1alpha1.DestinationCredentials{im.Spec.Destination.Manage, im.Spec.Destination.Pull} {
		if credentials == nil {
			continue
		}
		_, err := r.resolver.Resolve(ctx, imagepath.Reference{}, &credentials.Auth, nil)
		var notFound *auth.ErrSecretNotFound
		var malformed *auth.ErrSecretMalformed
		manage := credentials == im.Spec.Destination.Manage
		switch {
		case err == nil:
		case errors.As(err, &notFound):
			return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretNotFound, Message: err.Error()}, manage, nil
		case errors.As(err, &malformed):
			return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretMalformed, Message: err.Error()}, manage, nil
		default:
			return nil, false, err
		}
	}
	return nil, false, nil
}

// SetupWithManager sets up the controller with the Manager. A copy or a check that ended
// asks for a reconcile of its mirror through a channel.
func (r *ImageMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	wakes := make(chan event.TypedGenericEvent[*kuikv1alpha1.ImageMirror], 1024)
	r.wakeFn = func(name string) {
		select {
		case wakes <- event.TypedGenericEvent[*kuikv1alpha1.ImageMirror]{Object: &kuikv1alpha1.ImageMirror{ObjectMeta: metav1.ObjectMeta{Name: name}}}:
		default:
			// A full channel already holds reconciles enough to pick this change up.
		}
	}
	if r.StatusInterval == 0 {
		r.StatusInterval = DefaultStatusInterval
	}
	// The reconciler reads Secrets in the cluster resource namespace only, so it watches them
	// through a cache of that namespace alone.
	secrets, err := cache.New(mgr.GetConfig(), cache.Options{
		Scheme:            mgr.GetScheme(),
		Mapper:            mgr.GetRESTMapper(),
		DefaultNamespaces: map[string]cache.Config{r.namespace: {}},
	})
	if err != nil {
		return err
	}
	if err := mgr.Add(secrets); err != nil {
		return err
	}
	// Runs once this replica holds the lease, which is when pod events may be emitted.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		r.Elected(time.Now())
		<-ctx.Done()
		return nil
	})); err != nil {
		return err
	}

	// Pod and CR events are debounced alike (docs/v3/spec.md, "mirror: pacing the destination
	// kuik owns"): a creation or a deletion of the mirror reconciles at once, a spec change
	// StatusInterval later.
	onlyCreateDelete := predicate.Funcs{UpdateFunc: func(event.UpdateEvent) bool { return false }}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMirror{}, builder.WithPredicates(onlyCreateDelete)).
		Watches(&kuikv1alpha1.ImageMirror{}, delayedUpdates(r.StatusInterval)).
		Watches(&corev1.Pod{}, delayed(r.StatusInterval, r.mirrorsSelecting)).
		Watches(&corev1.Namespace{}, delayed(r.StatusInterval, r.allMirrors)).
		WatchesRawSource(source.Kind[client.Object](secrets, &corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mirrorsNaming))).
		WatchesRawSource(source.Channel(wakes, &handler.TypedEnqueueRequestForObject[*kuikv1alpha1.ImageMirror]{})).
		Named("kuik-imagemirror").
		Complete(r)
}

// delayedUpdates enqueues an ImageMirror whose spec changed, after interval.
func delayedUpdates(interval time.Duration) handler.EventHandler {
	return handler.Funcs{
		UpdateFunc: func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
				q.AddAfter(reconcile.Request{NamespacedName: types.NamespacedName{Name: e.ObjectNew.GetName()}}, interval)
			}
		},
	}
}

// mirrorsSelecting maps a pod to the ImageMirrors that select it.
func (r *ImageMirrorReconciler) mirrorsSelecting(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Namespace}, &ns); err != nil {
		// Without the namespace labels, every mirror may select the pod.
		return r.allMirrors(ctx, obj)
	}
	var list kuikv1alpha1.ImageMirrorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for i := range list.Items {
		// A deleted mirror waits for the pods running its copies: it hears of them all.
		s, _ := mirrorScope(&list.Items[i])
		if s.selects(pod, ns.Labels) || !list.Items[i].DeletionTimestamp.IsZero() {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
	}
	return requests
}

// allMirrors maps any object to every ImageMirror.
func (r *ImageMirrorReconciler) allMirrors(ctx context.Context, _ client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageMirrorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, im := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: im.Name}})
	}
	return requests
}

// mirrorsNaming maps a Secret to the ImageMirrors whose manage or pull secretRef names it.
func (r *ImageMirrorReconciler) mirrorsNaming(ctx context.Context, obj client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageMirrorList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, im := range list.Items {
		for _, credentials := range []*kuikv1alpha1.DestinationCredentials{im.Spec.Destination.Manage, im.Spec.Destination.Pull} {
			if credentials != nil && credentials.Auth.SecretRef != nil && credentials.Auth.SecretRef.Name == obj.GetName() {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: im.Name}})
				break
			}
		}
	}
	return requests
}
