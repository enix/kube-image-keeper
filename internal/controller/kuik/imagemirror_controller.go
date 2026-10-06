package kuik

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
}

// NewImageMirrorReconciler returns a reconciler writing with c.
func NewImageMirrorReconciler(c client.Client, scheme *runtime.Scheme, opts ImageMirrorOptions) (*ImageMirrorReconciler, error) {
	tracker, err := routingstatus.NewTracker(opts.Recorder, opts.Registerer)
	if err != nil {
		return nil, err
	}
	limiter, err := capped.NewLimiter(opts.Registerer)
	if err != nil {
		return nil, err
	}
	readiness, err := condition.NewReadiness(opts.Recorder, opts.Registerer)
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
	mirror := plan.Mirror{Path: im.Spec.Destination.Path, ExcludeImages: im.Spec.ExcludeImages, CleanupEnabled: im.Spec.Cleanup.IsEnabled()}
	desired := plan.Desired(mirror, pods, im.Status.PendingDeletion)
	if !blocked {
		sources, err := r.copySources(ctx, desired, pods)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.owe(&im, st, st.owed(desired), sources)
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
	next.FailedImageCopies = r.reportFailures(&im, st, desired)
	next.FailedImageCopies = capped.Cap(pass, "failedImageCopies", next.FailedImageCopies,
		func(e kuikv1alpha1.FailedImageCopy) metav1.Time { return e.Since },
		func(e kuikv1alpha1.FailedImageCopy) string { return e.Ref })
	live := plan.Desired(plan.Mirror{Path: mirror.Path, ExcludeImages: mirror.ExcludeImages}, pods, nil)
	counts := plan.Counts(plan.Observed{
		ClusterID: r.config.Load().ClusterID,
		Path:      mirror.Path,
		Live:      live,
		Images:    liveImages(pods),
		Copied:    st.copiedSet(),
		Status:    next,
	})
	next.Images = &kuikv1alpha1.MirrorImages{Copy: &counts}
	next.Truncated = pass.End(&next.Conditions, im.Generation)
	r.readiness.Set(&im, resource.Kind, resource.Name, &next.Conditions, im.Generation, notReady)

	if equality.Semantic.DeepEqual(im.Status, next) {
		return ctrl.Result{}, nil
	}
	im.Status = next
	if err := r.Status().Update(ctx, &im); err != nil {
		// A copy records its repository in the same status: the next reconcile reads it.
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: conflictWait}, nil
		}
		return ctrl.Result{}, err
	}
	logf.FromContext(ctx).V(1).Info("Updated ImageMirror status")
	return ctrl.Result{}, nil
}

func (r *ImageMirrorReconciler) forget(resource routing.Resource) {
	r.tracker.Forget(resource)
	r.limiter.Forget(resource.Kind, resource.Name)
	r.readiness.Forget(resource.Kind, resource.Name)
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMirror{}).
		WatchesRawSource(source.Channel(wakes, &handler.TypedEnqueueRequestForObject[*kuikv1alpha1.ImageMirror]{})).
		Named("kuik-imagemirror").
		Complete(r)
}
