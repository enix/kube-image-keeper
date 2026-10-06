package kuik

import (
	"context"
	"errors"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/imagepath"
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
	resolver  *auth.Resolver
	tracker   *routingstatus.Tracker
	limiter   *capped.Limiter
	readiness *condition.Readiness
	scheduler *pacing.Scheduler
	registry  *registry.Client
	clock     clock.Clock
	config    atomic.Pointer[config.Config]
	// elected is set once the lease is held; no status is written before it.
	elected atomic.Bool
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
		resolver:  auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeWebhook),
		tracker:   tracker,
		limiter:   limiter,
		readiness: readiness,
		scheduler: opts.Scheduler,
		registry:  opts.Registry,
		clock:     opts.Clock,
	}
	r.config.Store(opts.Config)
	return r, nil
}

// SetConfig applies a reloaded global config.
func (r *ImageMirrorReconciler) SetConfig(cfg *config.Config) {
	r.config.Store(cfg)
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
	if notReady == nil {
		var err error
		if notReady, err = r.checkSecrets(ctx, &im); err != nil {
			return ctrl.Result{}, err
		}
	}
	pods, err := selectedPods(ctx, r, scope)
	if err != nil {
		return ctrl.Result{}, err
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
	next.Truncated = pass.End(&next.Conditions, im.Generation)
	r.readiness.Set(&im, resource.Kind, resource.Name, &next.Conditions, im.Generation, notReady)

	if equality.Semantic.DeepEqual(im.Status, next) {
		return ctrl.Result{}, nil
	}
	im.Status = next
	if err := r.Status().Update(ctx, &im); err != nil {
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
func (r *ImageMirrorReconciler) checkSecrets(ctx context.Context, im *kuikv1alpha1.ImageMirror) (*condition.NotReady, error) {
	for _, credentials := range []*kuikv1alpha1.DestinationCredentials{im.Spec.Destination.Manage, im.Spec.Destination.Pull} {
		if credentials == nil {
			continue
		}
		_, err := r.resolver.Resolve(ctx, imagepath.Reference{}, &credentials.Auth, nil)
		var notFound *auth.ErrSecretNotFound
		var malformed *auth.ErrSecretMalformed
		switch {
		case err == nil:
		case errors.As(err, &notFound):
			return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretNotFound, Message: err.Error()}, nil
		case errors.As(err, &malformed):
			return &condition.NotReady{Reason: kuikv1alpha1.ReasonSecretMalformed, Message: err.Error()}, nil
		default:
			return nil, err
		}
	}
	return nil, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageMirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageMirror{}).
		Named("kuik-imagemirror").
		Complete(r)
}
