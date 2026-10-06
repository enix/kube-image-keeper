// Package secretsyncer materialises the pull Secrets the Pod webhook injects: one Secret per
// pair (routing resource, namespace), applied blind and kept valid. See
// docs/v3/walkthroughs/03-secret-syncer-reconciliation.md.
package secretsyncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth/pullsecret"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

// DefaultDebounce is how long the events of one pair are collapsed into one reconcile: a
// rollout is a burst of pod creates and deletes that almost always leaves the pair unchanged.
const DefaultDebounce = time.Second

// EventPullSecretInjectionFailed is emitted on a resource whose credential could not be
// resolved for a pair.
const EventPullSecretInjectionFailed = "PullSecretInjectionFailed"

// graceRecheck is how often a pair holding an entry in its grace period is reconciled again,
// so that the entry is dropped soon after the period ends.
const graceRecheck = time.Minute

// The results of kuik_secret_applies_total.
const (
	resultApplied = "Applied"
	resultNoop    = "Noop"
	resultFailed  = "Failed"
)

// Options are what a Syncer reads and reports with.
type Options struct {
	// Namespace is the install namespace, where a secretRef resolves.
	Namespace string
	// Recorder emits the events on the resources.
	Recorder events.EventRecorder
	// Registerer exports kuik_secret_applies_total.
	Registerer prometheus.Registerer
	// Debounce delays the reconcile of a pair. Zero means DefaultDebounce.
	Debounce time.Duration
	// Now reads the time the grace period is counted with. Nil means time.Now.
	Now func() time.Time
}

// Syncer applies the pull Secret of every pair. Everything it holds in memory is rebuilt
// from the informers, or degrades safely when lost: see the cross-cutting invariants of the
// walkthrough.
type Syncer struct {
	client.Client

	namespace string
	recorder  events.EventRecorder
	debounce  time.Duration
	grace     *pullsecret.Grace
	applies   *prometheus.CounterVec

	mu sync.Mutex
	// known are the pairs this process applied a Secret for, so that it can still empty them.
	known map[pullsecret.Pair]*applied
	// forced are the pairs a resync asked to re-apply whatever their hash.
	forced map[pullsecret.Pair]bool
	// reported are the failures already announced, per pair.
	reported map[pullsecret.Pair][]pullsecret.Failure
}

// applied is what the last apply of a pair left.
type applied struct {
	// hash identifies the desired Secret that was applied.
	hash string
	// resourceVersion is the one the server answered with.
	resourceVersion string
	// sources are the source Secrets the pair was built from.
	sources []string
}

// New returns a syncer writing with c, which must read from a cache built with CacheOptions.
func New(c client.Client, opts Options) (*Syncer, error) {
	applies := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kuik_secret_applies_total",
		Help: "Pull-secret applies performed by the syncer, by outcome (Applied, Noop, Failed)",
	}, []string{"result"})
	if err := opts.Registerer.Register(applies); err != nil {
		return nil, err
	}
	for _, result := range []string{resultApplied, resultNoop, resultFailed} {
		applies.WithLabelValues(result)
	}

	debounce := opts.Debounce
	if debounce == 0 {
		debounce = DefaultDebounce
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Syncer{
		Client:    c,
		namespace: opts.Namespace,
		recorder:  opts.Recorder,
		debounce:  debounce,
		grace:     pullsecret.NewGrace(now),
		applies:   applies,
		known:     map[pullsecret.Pair]*applied{},
		forced:    map[pullsecret.Pair]bool{},
		reported:  map[pullsecret.Pair][]pullsecret.Failure{},
	}, nil
}

// CacheOptions are the cache options of the syncer's manager: Secrets are watched in the
// install namespace only, the one namespace the syncer may read them in.
func CacheOptions(namespace string) cache.Options {
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.Secret{}: {Namespaces: map[string]cache.Config{namespace: {}}},
	}}
}

// SetupWithManager registers the syncer and its watches. The informers' initial lists
// enqueue every pair, which is the startup pass.
func (s *Syncer) SetupWithManager(mgr ctrl.Manager) error {
	return builder.TypedControllerManagedBy[pullsecret.Pair](mgr).
		Named("kuik-secret-syncer").
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &kuikv1alpha1.ImageAlternative{},
			resourceHandler[*kuikv1alpha1.ImageAlternative](s, routing.KindImageAlternative))).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &kuikv1alpha1.ImageMirror{},
			resourceHandler[*kuikv1alpha1.ImageMirror](s, routing.KindImageMirror))).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Namespace{}, s.namespaceHandler())).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{}, s.podHandler())).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Secret{}, s.sourceHandler())).
		Complete(s)
}

// The syncer reads the three kinds, the pods and the namespaces cluster-wide, and
// Secrets in the install namespace only. It writes Secrets everywhere, and never reads one it
// wrote: see docs/v3/architecture.md, "Permissions". Its events go through the
// events.k8s.io API, on cluster-scoped resources.
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives;imagemirrors;imagemonitors,verbs=get;list;watch,roleName=secret-syncer
// +kubebuilder:rbac:groups="",resources=pods;namespaces,verbs=get;list;watch,roleName=secret-syncer
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=secret-syncer
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;patch,roleName=secret-syncer
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch,roleName=secret-syncer

// Reconcile applies the Secret of one pair.
func (s *Syncer) Reconcile(ctx context.Context, pair pullsecret.Pair) (reconcile.Result, error) {
	log := logf.FromContext(ctx).WithValues("resource", pair.Resource.String(), "namespace", pair.Namespace)

	r, object, err := s.resource(ctx, pair.Resource)
	if apierrors.IsNotFound(err) {
		// The owner reference collects the Secrets of a deleted resource.
		s.forget(pair)
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	var ns corev1.Namespace
	if err := s.Get(ctx, types.NamespacedName{Name: pair.Namespace}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			s.forget(pair)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if ns.DeletionTimestamp != nil {
		s.forget(pair)
		return reconcile.Result{}, nil
	}
	var podList corev1.PodList
	if err := s.List(ctx, &podList, client.InNamespace(pair.Namespace)); err != nil {
		return reconcile.Result{}, err
	}
	pods := make([]*corev1.Pod, 0, len(podList.Items))
	for i := range podList.Items {
		pods = append(pods, &podList.Items[i])
	}

	needed := r.Needed(&ns, pods)
	var result reconcile.Result
	if r.Policy == kuikv1alpha1.RewritePolicyOnFailure {
		retained := s.grace.Retain(pair, needed)
		if len(retained) > len(needed) {
			result.RequeueAfter = graceRecheck
		}
		needed = retained
	} else {
		s.grace.Forget(pair)
	}

	s.mu.Lock()
	last := s.known[pair]
	force := s.forced[pair]
	delete(s.forced, pair)
	s.mu.Unlock()
	if len(needed) == 0 && last == nil {
		// Nothing to provision, and no Secret of this process to empty.
		return result, nil
	}

	var unreadable error
	secret, failures := pullsecret.Build(r, pair.Namespace, needed, s.source(ctx, &unreadable))
	if unreadable != nil {
		return reconcile.Result{}, unreadable
	}
	s.report(pair, object, failures)
	hash, err := hashOf(secret)
	if err != nil {
		return reconcile.Result{}, err
	}
	sources := make([]string, 0, len(needed))
	for _, c := range needed {
		sources = append(sources, c.SecretRef)
	}
	if !force && last != nil && last.hash == hash {
		// An entry whose source is missing leaves the Secret unchanged, yet its source must
		// be watched for: its creation re-applies the pair.
		s.mu.Lock()
		last.sources = sources
		s.mu.Unlock()
		return result, nil
	}

	if err := s.Apply(ctx, secret, client.FieldOwner(pullsecret.ManagedBy), client.ForceOwnership); err != nil {
		s.applies.WithLabelValues(resultFailed).Inc()
		return reconcile.Result{}, fmt.Errorf("applying the pull Secret: %w", err)
	}
	// The server answers an apply that changed nothing with the same resourceVersion. The
	// first apply of a pair after a restart has nothing to compare with, and counts as
	// Applied.
	outcome := resultApplied
	if last != nil && secret.ResourceVersion != nil && *secret.ResourceVersion == last.resourceVersion {
		outcome = resultNoop
	}
	s.applies.WithLabelValues(outcome).Inc()
	// A changed Secret is a state change, logged at Info. The first apply after a restart may
	// have changed nothing, and every pair takes one: it stays at V(1).
	if outcome == resultApplied {
		applyLog := log
		if last == nil {
			applyLog = log.V(1)
		}
		applyLog.Info("Applied pull Secret", "secret", klog.KRef(pair.Namespace, *secret.Name))
	}

	s.mu.Lock()
	s.known[pair] = &applied{hash: hash, resourceVersion: ptrValue(secret.ResourceVersion), sources: sources}
	s.mu.Unlock()
	return result, nil
}

// resource reads a routing resource from the cache.
func (s *Syncer) resource(ctx context.Context, res routing.Resource) (pullsecret.Resource, client.Object, error) {
	key := types.NamespacedName{Name: res.Name}
	switch res.Kind {
	case routing.KindImageAlternative:
		var cr kuikv1alpha1.ImageAlternative
		if err := s.Get(ctx, key, &cr); err != nil {
			return pullsecret.Resource{}, nil, err
		}
		return pullsecret.FromImageAlternative(&cr), &cr, nil
	case routing.KindImageMirror:
		var cr kuikv1alpha1.ImageMirror
		if err := s.Get(ctx, key, &cr); err != nil {
			return pullsecret.Resource{}, nil, err
		}
		return pullsecret.FromImageMirror(&cr), &cr, nil
	default:
		return pullsecret.Resource{}, nil, fmt.Errorf("unknown routing kind %q", res.Kind)
	}
}

// source looks a source Secret up in the install namespace, from the cache. Only a Secret
// that does not exist reads as missing: any other error is collected in failed, for the
// reconcile to fail and retry rather than apply a Secret without that credential.
func (s *Syncer) source(ctx context.Context, failed *error) pullsecret.Sources {
	return func(name string) (*corev1.Secret, bool) {
		var secret corev1.Secret
		err := s.Get(ctx, types.NamespacedName{Namespace: s.namespace, Name: name}, &secret)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				*failed = errors.Join(*failed, fmt.Errorf("reading Secret %q: %w", name, err))
			}
			return nil, false
		}
		return &secret, true
	}
}

// report emits PullSecretInjectionFailed for each failure of the pair not announced yet: an
// event fires when something becomes true, not while it stays true.
func (s *Syncer) report(pair pullsecret.Pair, object client.Object, failures []pullsecret.Failure) {
	s.mu.Lock()
	previous := s.reported[pair]
	if len(failures) == 0 {
		delete(s.reported, pair)
	} else {
		s.reported[pair] = failures
	}
	s.mu.Unlock()

	for _, f := range failures {
		if slices.Contains(previous, f) {
			continue
		}
		s.recorder.Eventf(object, nil, corev1.EventTypeWarning, EventPullSecretInjectionFailed, "Sync",
			"%s: %s, for %s in namespace %s", f.Reason, f.Message, f.Credential.Path, pair.Namespace)
	}
}

// forget drops everything held for a pair that no longer exists.
func (s *Syncer) forget(pair pullsecret.Pair) {
	s.grace.Forget(pair)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.known, pair)
	delete(s.forced, pair)
	delete(s.reported, pair)
}

// hashOf identifies a desired Secret.
func hashOf(secret any) (string, error) {
	data, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func ptrValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// queue is the work queue of the pairs.
type queue = workqueue.TypedRateLimitingInterface[pullsecret.Pair]

// enqueue adds pairs after the debounce: a pair already waiting is not added twice.
func (s *Syncer) enqueue(q queue, pairs ...pullsecret.Pair) {
	for _, p := range pairs {
		q.AddAfter(p, s.debounce)
	}
}

// pairsOf returns the pairs of a resource: one per namespace of the cluster, the reconcile
// deciding whether it needs a Secret.
func (s *Syncer) pairsOf(ctx context.Context, res routing.Resource) []pullsecret.Pair {
	var namespaces corev1.NamespaceList
	if err := s.List(ctx, &namespaces); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Namespaces")
		return nil
	}
	pairs := make([]pullsecret.Pair, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		pairs = append(pairs, pullsecret.Pair{Resource: res, Namespace: ns.Name})
	}
	return pairs
}

// resourceHandler enqueues the pairs of a routing resource. An update that changes nothing is
// the informer's resync: its pairs are re-applied whatever their hash, which bounds how long
// someone else's edit of a managed Secret survives.
func resourceHandler[T client.Object](s *Syncer, kind string) handler.TypedEventHandler[T, pullsecret.Pair] {
	pairs := func(ctx context.Context, object T) []pullsecret.Pair {
		return s.pairsOf(ctx, routing.Resource{Kind: kind, Name: object.GetName()})
	}
	return handler.TypedFuncs[T, pullsecret.Pair]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[T], q queue) {
			s.enqueue(q, pairs(ctx, e.Object)...)
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[T], q queue) {
			list := pairs(ctx, e.ObjectNew)
			if e.ObjectOld.GetResourceVersion() == e.ObjectNew.GetResourceVersion() {
				s.mu.Lock()
				for _, p := range list {
					if s.known[p] != nil {
						s.forced[p] = true
					}
				}
				s.mu.Unlock()
			}
			s.enqueue(q, list...)
		},
		DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[T], q queue) {
			s.enqueue(q, pairs(ctx, e.Object)...)
		},
	}
}

// namespaceHandler enqueues the pairs of a namespace, one per routing resource.
func (s *Syncer) namespaceHandler() handler.TypedEventHandler[*corev1.Namespace, pullsecret.Pair] {
	pairs := func(ctx context.Context, ns *corev1.Namespace) []pullsecret.Pair {
		var alternatives kuikv1alpha1.ImageAlternativeList
		var mirrors kuikv1alpha1.ImageMirrorList
		if err := s.List(ctx, &alternatives); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to list ImageAlternatives")
		}
		if err := s.List(ctx, &mirrors); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to list ImageMirrors")
		}
		list := make([]pullsecret.Pair, 0, len(alternatives.Items)+len(mirrors.Items))
		for _, cr := range alternatives.Items {
			list = append(list, pullsecret.Pair{Resource: routing.Resource{Kind: routing.KindImageAlternative, Name: cr.Name}, Namespace: ns.Name})
		}
		for _, cr := range mirrors.Items {
			list = append(list, pullsecret.Pair{Resource: routing.Resource{Kind: routing.KindImageMirror, Name: cr.Name}, Namespace: ns.Name})
		}
		return list
	}
	return handler.TypedFuncs[*corev1.Namespace, pullsecret.Pair]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*corev1.Namespace], q queue) {
			s.enqueue(q, pairs(ctx, e.Object)...)
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*corev1.Namespace], q queue) {
			s.enqueue(q, pairs(ctx, e.ObjectNew)...)
		},
		DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[*corev1.Namespace], q queue) {
			s.enqueue(q, pairs(ctx, e.Object)...)
		},
	}
}

// podHandler enqueues the pairs a pod's rewrites name, before and after the change.
func (s *Syncer) podHandler() handler.TypedEventHandler[*corev1.Pod, pullsecret.Pair] {
	pairs := func(pods ...*corev1.Pod) []pullsecret.Pair {
		var list []pullsecret.Pair
		for _, pod := range pods {
			// A malformed annotation reads as empty: the pod then names no pair.
			records, _ := podrecord.Read(pod)
			for _, rewrite := range records.Rewrites {
				res, err := routing.ParseResource(rewrite.By)
				if err != nil {
					continue
				}
				if p := (pullsecret.Pair{Resource: res, Namespace: pod.Namespace}); !slices.Contains(list, p) {
					list = append(list, p)
				}
			}
		}
		return list
	}
	return handler.TypedFuncs[*corev1.Pod, pullsecret.Pair]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[*corev1.Pod], q queue) {
			s.enqueue(q, pairs(e.Object)...)
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[*corev1.Pod], q queue) {
			s.enqueue(q, pairs(e.ObjectOld, e.ObjectNew)...)
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[*corev1.Pod], q queue) {
			s.enqueue(q, pairs(e.Object)...)
		},
	}
}

// sourceHandler enqueues the pairs built from a source Secret, from the map the applies
// left: no lookup in the cluster.
func (s *Syncer) sourceHandler() handler.TypedEventHandler[*corev1.Secret, pullsecret.Pair] {
	pairs := func(secret *corev1.Secret) []pullsecret.Pair {
		s.mu.Lock()
		defer s.mu.Unlock()
		var list []pullsecret.Pair
		for p, a := range s.known {
			if slices.Contains(a.sources, secret.Name) {
				list = append(list, p)
			}
		}
		return list
	}
	return handler.TypedFuncs[*corev1.Secret, pullsecret.Pair]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[*corev1.Secret], q queue) {
			s.enqueue(q, pairs(e.Object)...)
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[*corev1.Secret], q queue) {
			s.enqueue(q, pairs(e.ObjectNew)...)
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[*corev1.Secret], q queue) {
			s.enqueue(q, pairs(e.Object)...)
		},
	}
}
