package kuik

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
	"github.com/enix/kube-image-keeper/internal/status/capped"
	"github.com/enix/kube-image-keeper/internal/status/condition"
	"github.com/enix/kube-image-keeper/internal/status/routingstatus"
)

// DefaultStatusInterval is how long a pod change waits before the status it affects is
// written: a rollout cycles through thousands of pods, and writing per event would storm the
// API server, so a burst of pod events writes each status once.
const DefaultStatusInterval = 10 * time.Second

// ImageAlternativeReconciler writes the status of the ImageAlternatives: whether each one is
// usable, and the routing side read off the pods it selects.
type ImageAlternativeReconciler struct {
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
	// elected is set once the lease is held; no status is written before it.
	elected atomic.Bool
}

// ImageAlternativeOptions are what an ImageAlternativeReconciler reads and reports with.
type ImageAlternativeOptions struct {
	// APIReader reads the Secrets a secretRef names, uncached.
	APIReader client.Reader
	// ClusterResourceNamespace is where a secretRef resolves.
	ClusterResourceNamespace string
	// Recorder emits the events, on the resources and on the pods.
	Recorder events.EventRecorder
	// Registerer exports the series.
	Registerer prometheus.Registerer
}

// NewImageAlternativeReconciler returns a reconciler writing with c.
func NewImageAlternativeReconciler(c client.Client, scheme *runtime.Scheme, opts ImageAlternativeOptions) (*ImageAlternativeReconciler, error) {
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
	return &ImageAlternativeReconciler{
		Client:    c,
		Scheme:    scheme,
		namespace: opts.ClusterResourceNamespace,
		// Webhook mode skips fallbackAuth: Ready answers for the Secrets the resource itself
		// names, never for the global config.
		resolver:  auth.NewResolver(opts.APIReader, opts.ClusterResourceNamespace, auth.ModeWebhook),
		tracker:   tracker,
		limiter:   limiter,
		readiness: readiness,
	}, nil
}

// Elected records when the lease was acquired: pod events go only to pods created since.
func (r *ImageAlternativeReconciler) Elected(at time.Time) {
	r.tracker.Elected(at)
	r.elected.Store(true)
}

// electionWait is how long a reconcile that runs before Elected waits before trying again.
const electionWait = time.Second

// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives/status,verbs=update;patch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=reconciler
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=reconciler
// The status events, on the resources and on the pods, go through the events.k8s.io API.
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch,roleName=reconciler

// Reconcile writes the status of one ImageAlternative.
func (r *ImageAlternativeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// A report before the lease time is known would persist a staleRewrites entry without its
	// RewriteStale, which the next leader would then never announce.
	if !r.elected.Load() {
		return ctrl.Result{RequeueAfter: electionWait}, nil
	}
	resource := routing.Resource{Kind: routing.KindImageAlternative, Name: req.Name}

	var ia kuikv1alpha1.ImageAlternative
	if err := r.Get(ctx, req.NamespacedName, &ia); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(resource)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	scope, notReady := newScope(&ia)
	if notReady == nil {
		var err error
		if notReady, err = r.checkSecrets(ctx, &ia); err != nil {
			return ctrl.Result{}, err
		}
	}
	pods, err := r.selectedPods(ctx, scope)
	if err != nil {
		return ctrl.Result{}, err
	}

	routingStatus := r.tracker.Report(routingstatus.Input{
		Resource: resource,
		Pods:     pods,
		Previous: ia.Status.RoutingStatus,
		Now:      metav1.Now(),
	})
	conditions := make([]metav1.Condition, len(ia.Status.Conditions))
	copy(conditions, ia.Status.Conditions)
	routingstatus.SetConditions(&conditions, resource.Kind, routingStatus, ia.Generation)
	pass := r.limiter.Begin(resource.Kind, resource.Name)
	routingstatus.Cap(pass, &routingStatus)
	truncated := pass.End(&conditions, ia.Generation)
	r.readiness.Set(&ia, resource.Kind, resource.Name, &conditions, ia.Generation, notReady)

	next := kuikv1alpha1.ImageAlternativeStatus{RoutingStatus: routingStatus, Truncated: truncated, Conditions: conditions}
	if equality.Semantic.DeepEqual(ia.Status, next) {
		return ctrl.Result{}, nil
	}
	ia.Status = next
	if err := r.Status().Update(ctx, &ia); err != nil {
		return ctrl.Result{}, err
	}
	logf.FromContext(ctx).V(1).Info("Updated ImageAlternative status")
	return ctrl.Result{}, nil
}

func (r *ImageAlternativeReconciler) forget(resource routing.Resource) {
	r.tracker.Forget(resource)
	r.limiter.Forget(resource.Kind, resource.Name)
	r.readiness.Forget(resource.Kind, resource.Name)
}

// scope is the pods a resource selects; a nil selector selects nothing.
type scope struct {
	pods, namespaces labels.Selector
}

func (s scope) selects(pod *corev1.Pod, namespaceLabels map[string]string) bool {
	return s.pods != nil && s.namespaces != nil &&
		s.pods.Matches(labels.Set(pod.Labels)) && s.namespaces.Matches(labels.Set(namespaceLabels))
}

// newScope parses the selectors of ia, and reports InvalidConfig when one of them, or one of
// its entries, does not parse.
func newScope(ia *kuikv1alpha1.ImageAlternative) (scope, *condition.NotReady) {
	pods, err := selector(ia.Spec.PodSelector)
	if err != nil {
		return scope{}, invalidConfig("podSelector", err)
	}
	namespaces, err := selector(ia.Spec.NamespaceSelector)
	if err != nil {
		return scope{}, invalidConfig("namespaceSelector", err)
	}
	if err := imagepath.ValidateAlternatives(ia.Spec.Alternatives); err != nil {
		return scope{pods: pods, namespaces: namespaces}, invalidConfig("alternatives", err)
	}
	return scope{pods: pods, namespaces: namespaces}, nil
}

// selector parses a label selector, an absent one matching everything.
func selector(s *metav1.LabelSelector) (labels.Selector, error) {
	if s == nil {
		return labels.Everything(), nil
	}
	return metav1.LabelSelectorAsSelector(s)
}

func invalidConfig(field string, err error) *condition.NotReady {
	return &condition.NotReady{Reason: kuikv1alpha1.ReasonInvalidConfig, Message: fmt.Sprintf("%s: %v", field, err)}
}

// checkSecrets reads the Secret of every secretRef of ia, and reports SecretNotFound or
// SecretMalformed for the first one that is missing or not a dockerconfigjson. A provider is
// deferred and reads nothing.
func (r *ImageAlternativeReconciler) checkSecrets(ctx context.Context, ia *kuikv1alpha1.ImageAlternative) (*condition.NotReady, error) {
	for i := range ia.Spec.Alternatives {
		entry := &ia.Spec.Alternatives[i]
		if entry.Auth == nil {
			continue
		}
		_, err := r.resolver.Resolve(ctx, imagepath.Reference{}, entry.Auth, nil)
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

// selectedPods returns the live pods scope selects.
func (r *ImageAlternativeReconciler) selectedPods(ctx context.Context, s scope) ([]*corev1.Pod, error) {
	var namespaces corev1.NamespaceList
	if err := r.List(ctx, &namespaces); err != nil {
		return nil, err
	}
	namespaceLabels := make(map[string]map[string]string, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		namespaceLabels[ns.Name] = ns.Labels
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return nil, err
	}
	var selected []*corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if attribution.Live(pod) && s.selects(pod, namespaceLabels[pod.Namespace]) {
			selected = append(selected, pod)
		}
	}
	return selected, nil
}

// SetupWithManager sets up the controller with the Manager: a spec change reports at once, a
// pod or namespace change after StatusInterval, a change to a Secret in the cluster resource
// namespace at once for the resources naming it.
func (r *ImageAlternativeReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&kuikv1alpha1.ImageAlternative{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Pod{}, delayed(r.StatusInterval, r.selecting)).
		Watches(&corev1.Namespace{}, delayed(r.StatusInterval, r.all)).
		WatchesRawSource(source.Kind[client.Object](secrets, &corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.naming))).
		Named("kuik-imagealternative").
		Complete(r)
}

// selecting maps a pod to the ImageAlternatives that select it.
func (r *ImageAlternativeReconciler) selecting(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Namespace}, &ns); err != nil {
		// Without the namespace labels, every resource may select the pod.
		return r.all(ctx, obj)
	}
	var list kuikv1alpha1.ImageAlternativeList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for i := range list.Items {
		if s, _ := newScope(&list.Items[i]); s.selects(pod, ns.Labels) {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
	}
	return requests
}

// all maps any object to every ImageAlternative.
func (r *ImageAlternativeReconciler) all(ctx context.Context, _ client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageAlternativeList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, ia := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: ia.Name}})
	}
	return requests
}

// naming maps a Secret to the ImageAlternatives whose secretRef names it.
func (r *ImageAlternativeReconciler) naming(ctx context.Context, obj client.Object) []reconcile.Request {
	var list kuikv1alpha1.ImageAlternativeList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, ia := range list.Items {
		for _, entry := range ia.Spec.Alternatives {
			if entry.Auth != nil && entry.Auth.SecretRef != nil && entry.Auth.SecretRef.Name == obj.GetName() {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: ia.Name}})
				break
			}
		}
	}
	return requests
}

// delayed enqueues what mapper returns after interval, the old and the new object of an
// update alike. The queue holds a key once, so the events of a burst write one status.
func delayed(interval time.Duration, mapper handler.MapFunc) handler.EventHandler {
	enqueue := func(ctx context.Context, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		for _, req := range mapper(ctx, obj) {
			q.AddAfter(req, interval)
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.ObjectOld, q)
			enqueue(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.Object, q)
		},
		GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, e.Object, q)
		},
	}
}
