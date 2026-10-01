package v1

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

// The annotations the webhook records its decisions in, keyed by container name. See
// docs/v3/observability.md, "Annotations".
const (
	AnnotationRewrites         = podrecord.AnnotationRewrites
	AnnotationConcededRewrites = podrecord.AnnotationConcededRewrites
	AnnotationNoAlternatives   = podrecord.AnnotationNoAlternatives
)

const (
	// annotationStaticPod marks the mirror pod of a static pod, which the webhook never
	// rewrites.
	annotationStaticPod = "kubernetes.io/config.mirror"
	// injectedSecretPrefix is the prefix auth.InjectedSecretName reserves to the syncer: a
	// pull secret carrying it is kuik's to add and remove. See docs/v3/architecture.md,
	// "The name of an injected Secret".
	injectedSecretPrefix = "kuik-inject-"
)

// Rewrite is one entry of kuik.enix.io/rewrites and kuik.enix.io/conceded-rewrites.
type Rewrite = podrecord.Rewrite

// The two counters the webhook exports. See docs/v3/observability.md, "Counters".
var (
	RewritesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kuik_routing_rewrites_total",
		Help: "Container images rewritten at admission, by the routing resource that supplied the reference " +
			"and the policy that placed it (Always, OnFailure)",
	}, []string{"kind", "name", "policy"})
	AlternativesExhaustedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kuik_routing_alternatives_exhausted_total",
		Help: "Containers left untouched at admission because no candidate answered, counted once per routing " +
			"resource that offered one. Several resources count the same container, so these series must not be summed",
	}, []string{"kind", "name"})
)

// Dependencies are what the webhook reads with.
type Dependencies struct {
	// Namespaces reads the Namespace of a pod, for the namespaceSelector of the resources.
	Namespaces client.Reader
	// Secrets reads Secrets. It must not be cached: a refused read of a pod's pull secret has
	// to come back as a Forbidden.
	Secrets client.Reader
	// ClusterResourceNamespace is where a `secretRef` resolves.
	ClusterResourceNamespace string
	// Registry probes the candidates.
	Registry *registry.Client
	// Config is the global config at startup.
	Config *config.Config
}

// SetupPodWebhookWithManager registers the webhook for Pod in the manager, keeps its
// resources in step with the manager's cache, and returns it so that config reloads reach it.
// It registers the two routing counters, and nothing else.
func SetupPodWebhookWithManager(mgr ctrl.Manager, cfg *config.Config, clusterResourceNamespace string) (*PodDefaulter, error) {
	for _, c := range []prometheus.Collector{RewritesTotal, AlternativesExhaustedTotal} {
		if err := metrics.Registry.Register(c); err != nil && !errors.As(err, &prometheus.AlreadyRegisteredError{}) {
			return nil, err
		}
	}
	d := NewPodDefaulter(Dependencies{
		Namespaces:               mgr.GetClient(),
		Secrets:                  mgr.GetAPIReader(),
		ClusterResourceNamespace: clusterResourceNamespace,
		Registry:                 registry.NewClient(),
		Config:                   cfg,
	})
	if err := mgr.Add(&resourceWatch{cache: mgr.GetCache(), defaulter: d}); err != nil {
		return nil, err
	}
	return d, ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(d).
		Complete()
}

// The three settings below are prescribed by docs/v3/architecture.md, "The admission path":
// fail open, CREATE only, reinvocable.
// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=ignore,reinvocationPolicy=IfNeeded,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

// The webhook reads what it matches pods against and writes nothing: no status, no Secret,
// no pod (the AdmissionReview carries it).
// +kubebuilder:rbac:groups=kuik.enix.io,resources=imagealternatives;imagemirrors;imagemonitors,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch,roleName=webhook
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=webhook

// Cluster-wide Secret read, for the imagePullSecrets of the pod an admission probe checks. The
// chart binds it according to secretAccess, to the webhook and the reconciler, never to the syncer.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,roleName=secret-reader

// PodDefaulter routes the images of a pod at admission. It writes nothing but the mutation
// it returns, and never fails an admission: kuik fails open.
type PodDefaulter struct {
	namespaces client.Reader
	resolver   *auth.Resolver
	registry   *registry.Client
	checks     *checkCache
	config     atomic.Pointer[config.Config]
	index      atomic.Pointer[routing.Index]
}

// NewPodDefaulter returns a webhook reading with d.
func NewPodDefaulter(d Dependencies) *PodDefaulter {
	w := &PodDefaulter{
		namespaces: d.Namespaces,
		resolver:   auth.NewResolver(d.Secrets, d.ClusterResourceNamespace, auth.ModeWebhook),
		registry:   d.Registry,
		checks:     newCheckCache(),
	}
	w.config.Store(d.Config)
	w.index.Store(routing.NewIndex(nil, nil))
	return w
}

// SetConfig replaces the global config, on every reload.
func (d *PodDefaulter) SetConfig(cfg *config.Config) {
	d.config.Store(cfg)
}

// SetResources replaces the routing resources the webhook matches pods against.
func (d *PodDefaulter) SetResources(alternatives []kuikv1alpha1.ImageAlternative, mirrors []kuikv1alpha1.ImageMirror) {
	d.index.Store(routing.NewIndex(alternatives, mirrors))
}

// pass is one pass of the webhook over a pod.
type pass struct {
	*PodDefaulter
	pod *corev1.Pod
	// probed is the pod as the credential resolution reads it, in the admission's namespace.
	probed          *corev1.Pod
	namespaceLabels map[string]string
	cfg             *config.Config
	index           *routing.Index
}

// Default implements admission.Defaulter so a webhook will be registered for the Kind Pod.
// See docs/v3/walkthroughs/01-routing-only.md, "2. Pod admission, mutating webhook".
func (d *PodDefaulter) Default(ctx context.Context, pod *corev1.Pod) error {
	if _, static := pod.Annotations[annotationStaticPod]; static {
		return nil
	}
	a := &pass{PodDefaulter: d, pod: pod, cfg: d.config.Load(), index: d.index.Load()}

	// A pod created through a namespaced endpoint may carry no namespace yet.
	probed := *pod
	if probed.Namespace == "" {
		if req, err := admission.RequestFromContext(ctx); err == nil {
			probed.Namespace = req.Namespace
		}
	}
	a.probed = &probed
	a.namespaceLabels = d.namespaceLabels(ctx, probed.Namespace)

	before := readRecords(ctx, pod)
	after := readRecords(ctx, pod)
	a.route(ctx, after)
	a.reconcilePullSecrets(after)
	after.Write(pod, before)
	return nil
}

func (d *PodDefaulter) namespaceLabels(ctx context.Context, namespace string) map[string]string {
	var ns corev1.Namespace
	if err := d.namespaces.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		logf.FromContext(ctx).V(1).Info("Failed to read Namespace, selecting on no label", "namespace", namespace, "error", err.Error())
		return nil
	}
	return ns.Labels
}

// containers are the routed containers of the pod: initContainers and containers, never
// ephemeralContainers.
func (a *pass) containers() []*corev1.Container {
	list := make([]*corev1.Container, 0, len(a.pod.Spec.InitContainers)+len(a.pod.Spec.Containers))
	for i := range a.pod.Spec.InitContainers {
		list = append(list, &a.pod.Spec.InitContainers[i])
	}
	for i := range a.pod.Spec.Containers {
		list = append(list, &a.pod.Spec.Containers[i])
	}
	return list
}

// pending is a new container waiting for its resolution.
type pending struct {
	container *corev1.Container
	origin    imagepath.Reference
	result    routing.Result
}

// route reads the state of every container off r and resolves the new ones. See
// docs/v3/architecture.md, "Recognising kuik's own output".
func (a *pass) route(ctx context.Context, r podrecord.Records) {
	containers := a.containers()
	present := map[string]bool{}
	for _, c := range containers {
		present[c.Name] = true
	}
	// gone: the container is no longer in the pod.
	for name := range r.Rewrites {
		if !present[name] {
			delete(r.Rewrites, name)
		}
	}
	for name := range r.Conceded {
		if !present[name] {
			delete(r.Conceded, name)
		}
	}
	for name := range r.NoAlternatives {
		if !present[name] {
			delete(r.NoAlternatives, name)
		}
	}

	var news []pending
	for _, c := range containers {
		if _, ok := r.Conceded[c.Name]; ok {
			continue
		}
		if entry, ok := r.Rewrites[c.Name]; ok {
			if c.Image != entry.RewrittenTo {
				// conceded: another webhook took the field over.
				r.Conceded[c.Name] = entry
				delete(r.Rewrites, c.Name)
			}
			continue
		}
		if _, ok := r.NoAlternatives[c.Name]; ok {
			continue
		}
		if p, ok := a.candidates(ctx, c); ok {
			news = append(news, p)
		}
	}
	a.resolve(ctx, news, r)
}

// candidates builds the candidate list of a new container, and reports false for one kuik
// leaves alone: imagePullPolicy Never, an image that does not parse, or no resource offering
// a candidate.
func (a *pass) candidates(ctx context.Context, c *corev1.Container) (pending, bool) {
	if c.ImagePullPolicy == corev1.PullNever {
		return pending{}, false
	}
	origin, err := imagepath.Parse(c.Image)
	if err != nil {
		logf.FromContext(ctx).V(1).Info("Left container with an unparsable image", "container", c.Name, "error", err.Error())
		return pending{}, false
	}
	result := a.index.Candidates(a.request(origin, c.ImagePullPolicy), a.options())
	if len(result.Offering) == 0 {
		return pending{}, false
	}
	return pending{container: c, origin: origin, result: result}, true
}

func (a *pass) request(image imagepath.Reference, pullPolicy corev1.PullPolicy) routing.Request {
	return routing.Request{
		Image:           image,
		PullPolicy:      pullPolicy,
		PodLabels:       a.pod.Labels,
		NamespaceLabels: a.namespaceLabels,
	}
}

func (a *pass) options() routing.Options {
	return routing.Options{
		ClusterID:                        a.cfg.ClusterID,
		DemoteMirrorWithPullPolicyAlways: a.cfg.Webhook.DemoteMirrorWithPullPolicyAlways,
	}
}

// resolve probes each distinct candidate list once, the lists concurrently and the
// candidates of one list in order, then rewrites and records the containers.
func (a *pass) resolve(ctx context.Context, news []pending, r podrecord.Records) {
	groups := map[string][]pending{}
	var keys []string
	for _, p := range news {
		key := listKey(p.result.Candidates)
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], p)
	}

	retained := make([]int, len(keys))
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Go(func() {
			retained[i] = a.firstAvailable(ctx, groups[key][0].result.Candidates)
		})
	}
	wg.Wait()

	for i, key := range keys {
		for _, p := range groups[key] {
			a.apply(p, retained[i], r)
		}
	}
}

// listKey identifies a candidate list: the references and configs, in order.
func listKey(candidates []routing.Candidate) string {
	// Candidates hold plain values only, so their JSON form cannot fail.
	key, _ := json.Marshal(candidates)
	return string(key)
}

// firstAvailable returns the index of the first candidate that answers, or -1.
func (a *pass) firstAvailable(ctx context.Context, candidates []routing.Candidate) int {
	for i, c := range candidates {
		if a.available(ctx, a.probed, c, a.cfg) {
			return i
		}
	}
	return -1
}

// apply records the outcome of a resolution on its container.
func (a *pass) apply(p pending, retained int, r podrecord.Records) {
	name := p.container.Name
	if retained < 0 {
		offering := make([]string, 0, len(p.result.Offering))
		for _, res := range p.result.Offering {
			offering = append(offering, res.String())
			AlternativesExhaustedTotal.WithLabelValues(res.Kind, res.Name).Inc()
		}
		r.NoAlternatives[name] = offering
		return
	}
	c := p.result.Candidates[retained]
	if c.Resource == nil {
		// The original answered: nothing happened, nothing is recorded.
		return
	}
	p.container.Image = c.Reference
	r.Rewrites[name] = Rewrite{
		By:          c.Resource.String(),
		Origin:      p.origin.String(),
		RewrittenTo: c.Reference,
		Policy:      string(c.Policy),
	}
	RewritesTotal.WithLabelValues(c.Resource.Kind, c.Resource.Name, string(c.Policy)).Inc()
}

// reconcilePullSecrets makes the pod carry a kuik-injected name if and only if a container
// still attributed to that resource in rewrites needs an injected credential, and never
// touches the pull secrets the pod declared. See docs/v3/architecture.md, "What conceding
// removes".
func (a *pass) reconcilePullSecrets(r podrecord.Records) {
	var needed []string
	for _, c := range a.containers() {
		entry, ok := r.Rewrites[c.Name]
		if !ok || !a.injects(entry, c.ImagePullPolicy) {
			continue
		}
		kind, name, _ := strings.Cut(entry.By, "/")
		if secret := auth.InjectedSecretName(kind, name); !slices.Contains(needed, secret) {
			needed = append(needed, secret)
		}
	}

	var secrets []corev1.LocalObjectReference
	for _, s := range a.pod.Spec.ImagePullSecrets {
		if strings.HasPrefix(s.Name, injectedSecretPrefix) {
			if !slices.Contains(needed, s.Name) || slices.Contains(secrets, s) {
				continue
			}
		}
		secrets = append(secrets, s)
	}
	for _, name := range needed {
		if s := (corev1.LocalObjectReference{Name: name}); !slices.Contains(secrets, s) {
			secrets = append(secrets, s)
		}
	}
	if !slices.Equal(secrets, a.pod.Spec.ImagePullSecrets) {
		a.pod.Spec.ImagePullSecrets = secrets
	}
}

// injects tells whether the candidate a rewrite placed carries an auth that injects, found
// again by rebuilding the candidate list of its origin from the current resources.
func (a *pass) injects(entry Rewrite, pullPolicy corev1.PullPolicy) bool {
	origin, err := imagepath.Parse(entry.Origin)
	if err != nil {
		return false
	}
	for _, c := range a.index.Candidates(a.request(origin, pullPolicy), a.options()).Candidates {
		if c.Resource != nil && c.Resource.String() == entry.By && c.Reference == entry.RewrittenTo {
			return c.Config.Auth.InjectsPullSecret()
		}
	}
	return false
}

// readRecords decodes the three annotation maps. A map that does not decode is read as
// empty, and rewritten on the way out.
func readRecords(ctx context.Context, pod *corev1.Pod) podrecord.Records {
	r, err := podrecord.Read(pod)
	if err != nil {
		logf.FromContext(ctx).V(1).Info("Ignored malformed annotation", "error", err.Error())
	}
	return r
}

// resourceWatch keeps the webhook's resources in step with the manager's cache: every event
// on an ImageAlternative or an ImageMirror rebuilds the index from the cache.
type resourceWatch struct {
	cache     cache.Cache
	defaulter *PodDefaulter
}

// Start rebuilds the index on every change until ctx is done.
func (w *resourceWatch) Start(ctx context.Context) error {
	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	handler := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { notify() },
		UpdateFunc: func(any, any) { notify() },
		DeleteFunc: func(any) { notify() },
	}
	for _, obj := range []client.Object{&kuikv1alpha1.ImageAlternative{}, &kuikv1alpha1.ImageMirror{}} {
		informer, err := w.cache.GetInformer(ctx, obj)
		if err != nil {
			return err
		}
		if _, err := informer.AddEventHandler(handler); err != nil {
			return err
		}
	}
	notify()

	log := logf.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
			var alternatives kuikv1alpha1.ImageAlternativeList
			var mirrors kuikv1alpha1.ImageMirrorList
			if err := w.cache.List(ctx, &alternatives); err != nil {
				log.Error(err, "Failed to list ImageAlternatives, kept the previous routing resources")
				continue
			}
			if err := w.cache.List(ctx, &mirrors); err != nil {
				log.Error(err, "Failed to list ImageMirrors, kept the previous routing resources")
				continue
			}
			w.defaulter.SetResources(alternatives.Items, mirrors.Items)
		}
	}
}

// NeedLeaderElection is false: every replica serves admissions.
func (w *resourceWatch) NeedLeaderElection() bool {
	return false
}
