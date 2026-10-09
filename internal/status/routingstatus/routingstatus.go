// Package routingstatus builds the routing side of a status, field for field the same on an
// ImageAlternative and an ImageMirror: the pods and containers gauges, the four anomaly lists,
// the FallbackActive and AlternativesExhausted conditions, the pod events and the routing
// series. See docs/v3/status.md, "ImageAlternative", and docs/v3/observability.md.
package routingstatus

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/status/attribution"
	"github.com/enix/kube-image-keeper/internal/status/capped"
	"github.com/enix/kube-image-keeper/internal/status/condition"
)

// The pod events. See docs/v3/observability.md, "Catalogue".
const (
	EventImageFallback          = "ImageFallback"
	EventNoAlternativeAvailable = "NoAlternativeAvailable"
	EventRewriteConceded        = "RewriteConceded"
	EventRewriteStale           = "RewriteStale"
)

// The capped lists, under their status field names.
const (
	listActiveFallbacks  = "activeFallbacks"
	listNoAlternatives   = "noAlternatives"
	listConcededRewrites = "concededRewrites"
	listStaleRewrites    = "staleRewrites"
)

// The labels of the routing series.
const (
	kindLabel     = "kind"
	nameLabel     = "name"
	stateLabel    = "state"
	imageLabel    = "image"
	registryLabel = "registry"
)

// eventAction is the action the pod events report.
const eventAction = "Route"

// Tracker reports the routing side of the resources of one controller. It remembers the pod
// events it emitted, so each is emitted once per lease.
type Tracker struct {
	recorder events.EventRecorder

	containers                 *prometheus.GaugeVec
	podsTracked, podsRewritten *prometheus.GaugeVec
	// anomalies are the four series that exist only while their anomaly does.
	anomalies [4]*prometheus.GaugeVec

	mu sync.Mutex
	// elected is when the lease was acquired, zero before.
	elected time.Time
	// announced holds, per resource, the pod events already emitted during this lease. A
	// resource without an entry has not been reported on yet in this lease.
	announced map[routing.Resource]map[announcement]bool
	// published holds, per resource, the labels of the anomaly series it exports.
	published map[routing.Resource][4]map[imageLabels]bool
}

// The anomaly series, in the order of Tracker.anomalies.
const (
	seriesFallback = iota
	seriesExhausted
	seriesConceded
	seriesStale
)

type announcement struct {
	pod       types.UID
	container string
	reason    string
}

type imageLabels struct{ image, registry string }

// NewTracker returns a tracker emitting with recorder and exporting to registerer.
func NewTracker(recorder events.EventRecorder, registerer prometheus.Registerer) (*Tracker, error) {
	resourceLabels := []string{kindLabel, nameLabel}
	imageLabelNames := []string{kindLabel, nameLabel, imageLabel, registryLabel}
	t := &Tracker{
		recorder: recorder,
		containers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_routing_containers",
			Help: "Containers of the pods a routing resource selects, by what it did to them",
		}, []string{kindLabel, nameLabel, stateLabel}),
		podsTracked: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_routing_pods_tracked",
			Help: "Live pods a routing resource's selectors retain",
		}, resourceLabels),
		podsRewritten: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_routing_pods_rewritten",
			Help: "Live pods carrying at least one container this resource rewrote, whose rewrite still stands",
		}, resourceLabels),
		anomalies: [4]*prometheus.GaugeVec{
			seriesFallback: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kuik_fallback_active_pods",
				Help: "Live pods routed to a fallback because the origin did not answer, under rewritePolicy: OnFailure. " +
					"image is the origin reference",
			}, imageLabelNames),
			seriesExhausted: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kuik_alternatives_exhausted_pods",
				Help: "Live pods carrying a container no candidate could serve. image is the origin reference. " +
					"Every resource that offered a candidate counts the pod, so these series must not be summed",
			}, imageLabelNames),
			seriesConceded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kuik_rewrite_conceded_pods",
				Help: "Live pods carrying a container this resource had rewritten and another mutating webhook replaced. " +
					"image is the origin reference",
			}, imageLabelNames),
			seriesStale: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kuik_rewrite_stale_pods",
				Help: "Live pods carrying a container this resource had rewritten and something replaced after admission, " +
					"so the record no longer describes what runs. image is the origin reference",
			}, imageLabelNames),
		},
		announced: map[routing.Resource]map[announcement]bool{},
		published: map[routing.Resource][4]map[imageLabels]bool{},
	}
	var err error
	if t.containers, err = register(registerer, t.containers); err != nil {
		return nil, err
	}
	if t.podsTracked, err = register(registerer, t.podsTracked); err != nil {
		return nil, err
	}
	if t.podsRewritten, err = register(registerer, t.podsRewritten); err != nil {
		return nil, err
	}
	for i := range t.anomalies {
		if t.anomalies[i], err = register(registerer, t.anomalies[i]); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// register registers c, or returns the collector already registered under its name: the
// ImageAlternative and ImageMirror controllers each build a tracker on the same registry.
func register(registerer prometheus.Registerer, c *prometheus.GaugeVec) (*prometheus.GaugeVec, error) {
	err := registerer.Register(c)
	if are := (prometheus.AlreadyRegisteredError{}); errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(*prometheus.GaugeVec); ok {
			return existing, nil
		}
	}
	return c, err
}

// Elected records when the reconciler acquired its lease. Before it, no pod event is emitted;
// after it, ImageFallback, NoAlternativeAvailable and RewriteConceded go only to pods created
// since.
func (t *Tracker) Elected(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.elected = at
	t.announced = map[routing.Resource]map[announcement]bool{}
}

// electionWait is how long a reconcile that runs before Elected waits before trying again.
const electionWait = time.Second

// ElectionWait returns how long a reconcile must wait before writing a status, zero once the
// lease is acquired. The controller only runs once the lease is held, but it and the runnable
// that calls Elected start concurrently, in no guaranteed order: the first reconciles may run
// before the lease time is known. A report then would persist a staleRewrites entry without
// its RewriteStale, which the next leader would never announce.
func (t *Tracker) ElectionWait() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.elected.IsZero() {
		return electionWait
	}
	return 0
}

// Input is what one report of a resource reads.
type Input struct {
	// Resource is the resource reported on.
	Resource routing.Resource
	// Pods are the live pods the resource selects.
	Pods []*corev1.Pod
	// Previous is the routing side of the status as last written, `since` being carried
	// forward from it.
	Previous kuikv1alpha1.RoutingStatus
	// Now stamps the entries that appear.
	Now metav1.Time
}

// The keys of the entries of the four lists.
type (
	fallbackKey struct{ image, rewrittenTo string }
	replacedKey struct{ image, rewrittenTo, replacedBy string }
)

// observed is a container of a pod as one report reads it.
type observed struct {
	pod       *corev1.Pod
	container attribution.Container
	state     attribution.State
}

// Report returns the routing side of the status, its lists whole and oldest first, emits the
// pod events and publishes the routing series, which hold every entry.
func (t *Tracker) Report(in Input) kuikv1alpha1.RoutingStatus {
	pods := kuikv1alpha1.PodCounts{}
	containers := kuikv1alpha1.ContainerCounts{}
	fallbacks := map[fallbackKey]map[types.UID]bool{}
	noAlternatives := map[string]map[types.UID]bool{}
	concededs := map[replacedKey]map[types.UID]bool{}
	stales := map[replacedKey]map[types.UID]bool{}
	var seen []observed

	for _, pod := range in.Pods {
		if attribution.Static(pod) {
			continue
		}
		pods.Tracked++
		rewritten := false
		for _, c := range attribution.Containers(pod) {
			state := c.StateFor(in.Resource)
			containers.Tracked++
			seen = append(seen, observed{pod: pod, container: c, state: state})
			switch state {
			case attribution.Untouched:
				containers.Untouched++
			case attribution.Rewritten:
				containers.Rewritten++
				rewritten = true
				if c.Record.Policy == string(kuikv1alpha1.RewritePolicyOnFailure) {
					add(fallbacks, fallbackKey{image: c.Record.Origin, rewrittenTo: c.Record.RewrittenTo}, pod.UID)
				}
			case attribution.Conceded:
				containers.Conceded++
				add(concededs, replacedKey{image: c.Record.Origin, rewrittenTo: c.Record.RewrittenTo, replacedBy: c.Image}, pod.UID)
			case attribution.Stale:
				containers.Stale++
				add(stales, replacedKey{image: c.Record.Origin, rewrittenTo: c.Record.RewrittenTo, replacedBy: c.Image}, pod.UID)
			case attribution.NoAlternatives:
				containers.NoAlternatives++
				add(noAlternatives, c.Origin, pod.UID)
			}
		}
		if rewritten {
			pods.Rewritten++
		}
	}

	status := kuikv1alpha1.RoutingStatus{
		Pods:             &pods,
		Containers:       &containers,
		ActiveFallbacks:  activeFallbacks(fallbacks, in.Previous.ActiveFallbacks, in.Now),
		NoAlternatives:   noAlternativeEntries(noAlternatives, in.Previous.NoAlternatives, in.Now),
		ConcededRewrites: replacedEntries(concededs, in.Previous.ConcededRewrites, in.Now),
		StaleRewrites:    replacedEntries(stales, in.Previous.StaleRewrites, in.Now),
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.emit(in, seen)
	t.publish(in.Resource, status, [4]map[imageLabels]map[types.UID]bool{
		seriesFallback:  byImage(fallbacks, func(k fallbackKey) string { return k.image }),
		seriesExhausted: byImage(noAlternatives, func(k string) string { return k }),
		seriesConceded:  byImage(concededs, func(k replacedKey) string { return k.image }),
		seriesStale:     byImage(stales, func(k replacedKey) string { return k.image }),
	})
	return status
}

func add[K comparable](m map[K]map[types.UID]bool, key K, pod types.UID) {
	if m[key] == nil {
		m[key] = map[types.UID]bool{}
	}
	m[key][pod] = true
}

func activeFallbacks(observed map[fallbackKey]map[types.UID]bool, previous []kuikv1alpha1.ActiveFallback, now metav1.Time) []kuikv1alpha1.ActiveFallback {
	since := map[fallbackKey]metav1.Time{}
	for _, e := range previous {
		since[fallbackKey{image: e.Image, rewrittenTo: e.RewrittenTo}] = e.Since
	}
	list := make([]kuikv1alpha1.ActiveFallback, 0, len(observed))
	for k, pods := range observed {
		list = append(list, kuikv1alpha1.ActiveFallback{
			Image: k.image, RewrittenTo: k.rewrittenTo, Pods: int32(len(pods)), Since: carried(since, k, now),
		})
	}
	slices.SortFunc(list, func(a, b kuikv1alpha1.ActiveFallback) int {
		return order(a.Since, b.Since, fallbackKeyOf(a), fallbackKeyOf(b))
	})
	return nilIfEmpty(list)
}

func noAlternativeEntries(observed map[string]map[types.UID]bool, previous []kuikv1alpha1.NoAlternative, now metav1.Time) []kuikv1alpha1.NoAlternative {
	since := map[string]metav1.Time{}
	for _, e := range previous {
		since[e.Image] = e.Since
	}
	list := make([]kuikv1alpha1.NoAlternative, 0, len(observed))
	for image, pods := range observed {
		list = append(list, kuikv1alpha1.NoAlternative{Image: image, Pods: int32(len(pods)), Since: carried(since, image, now)})
	}
	slices.SortFunc(list, func(a, b kuikv1alpha1.NoAlternative) int {
		return order(a.Since, b.Since, a.Image, b.Image)
	})
	return nilIfEmpty(list)
}

func replacedEntries(observed map[replacedKey]map[types.UID]bool, previous []kuikv1alpha1.ReplacedRewrite, now metav1.Time) []kuikv1alpha1.ReplacedRewrite {
	since := map[replacedKey]metav1.Time{}
	for _, e := range previous {
		since[replacedKeyOf(e)] = e.Since
	}
	list := make([]kuikv1alpha1.ReplacedRewrite, 0, len(observed))
	for k, pods := range observed {
		list = append(list, kuikv1alpha1.ReplacedRewrite{
			Image: k.image, RewrittenTo: k.rewrittenTo, ReplacedBy: k.replacedBy, Pods: int32(len(pods)), Since: carried(since, k, now),
		})
	}
	slices.SortFunc(list, func(a, b kuikv1alpha1.ReplacedRewrite) int {
		return order(a.Since, b.Since, keyOfReplaced(a), keyOfReplaced(b))
	})
	return nilIfEmpty(list)
}

// carried is the since of key in the previous status, or now for a new entry.
func carried[K comparable](since map[K]metav1.Time, key K, now metav1.Time) metav1.Time {
	if s, ok := since[key]; ok {
		return s
	}
	return now
}

// order sorts oldest first, then by key, so the list written does not churn.
func order(a, b metav1.Time, keyA, keyB string) int {
	if c := a.Compare(b.Time); c != 0 {
		return c
	}
	return cmp.Compare(keyA, keyB)
}

func nilIfEmpty[T any](list []T) []T {
	if len(list) == 0 {
		return nil
	}
	return list
}

func fallbackKeyOf(e kuikv1alpha1.ActiveFallback) string {
	return e.Image + "\x00" + e.RewrittenTo
}

func replacedKeyOf(e kuikv1alpha1.ReplacedRewrite) replacedKey {
	return replacedKey{image: e.Image, rewrittenTo: e.RewrittenTo, replacedBy: e.ReplacedBy}
}

func keyOfReplaced(e kuikv1alpha1.ReplacedRewrite) string {
	return e.Image + "\x00" + e.RewrittenTo + "\x00" + e.ReplacedBy
}

// byImage groups the pods of the entries per origin image, a pod counting once per image
// whatever the number of entries of that image it appears in.
func byImage[K comparable](entries map[K]map[types.UID]bool, image func(K) string) map[imageLabels]map[types.UID]bool {
	grouped := map[imageLabels]map[types.UID]bool{}
	for k, pods := range entries {
		labels := imageLabels{image: image(k), registry: registryOf(image(k))}
		if grouped[labels] == nil {
			grouped[labels] = map[types.UID]bool{}
		}
		for pod := range pods {
			grouped[labels][pod] = true
		}
	}
	return grouped
}

// registryOf is the host of image, empty when it does not parse.
func registryOf(image string) string {
	ref, err := imagepath.Parse(image)
	if err != nil {
		return ""
	}
	return ref.Host
}

// emit emits the pod events of one report, once per pod, container and reason during a
// lease. Called with t.mu held.
func (t *Tracker) emit(in Input, seen []observed) {
	if t.elected.IsZero() {
		return
	}
	announced, reported := t.announced[in.Resource]
	if !reported {
		// First report of this lease: the stale entries the previous status already lists
		// were announced by the previous leader, or by this one before a restart.
		announced = map[announcement]bool{}
		listed := map[replacedKey]bool{}
		for _, e := range in.Previous.StaleRewrites {
			listed[replacedKeyOf(e)] = true
		}
		for _, o := range seen {
			if o.state == attribution.Stale &&
				listed[replacedKey{image: o.container.Record.Origin, rewrittenTo: o.container.Record.RewrittenTo, replacedBy: o.container.Image}] {
				announced[announcement{pod: o.pod.UID, container: o.container.Name, reason: EventRewriteStale}] = true
			}
		}
	}

	// Only the announcements of the pods still seen are kept, so the memory follows the pods.
	kept := map[announcement]bool{}
	for _, o := range seen {
		reason, eventType, note := t.event(in.Resource, o)
		if reason == "" {
			continue
		}
		key := announcement{pod: o.pod.UID, container: o.container.Name, reason: reason}
		kept[key] = true
		if announced[key] {
			continue
		}
		t.recorder.Eventf(o.pod, nil, eventType, reason, eventAction, "%s", note)
	}
	t.announced[in.Resource] = kept
}

// event returns the pod event the container calls for from resource, if any.
func (t *Tracker) event(resource routing.Resource, o observed) (reason, eventType, note string) {
	c := o.container
	// creationTimestamp has a resolution of one second: a pod created in the second the lease
	// was acquired counts as created after it, rather than losing its event.
	afterLease := !o.pod.CreationTimestamp.Before(&metav1.Time{Time: t.elected.Truncate(time.Second)})
	switch o.state {
	case attribution.Rewritten:
		if afterLease && c.Record.Policy == string(kuikv1alpha1.RewritePolicyOnFailure) {
			return EventImageFallback, corev1.EventTypeNormal, fmt.Sprintf(
				"Container %s: origin %s did not answer, rewritten to %s supplied by %s",
				c.Name, c.Record.Origin, c.Record.RewrittenTo, c.Record.By)
		}
	case attribution.NoAlternatives:
		if afterLease && len(c.Offering) > 0 && c.Offering[0] == resource.String() {
			return EventNoAlternativeAvailable, corev1.EventTypeWarning, fmt.Sprintf(
				"Container %s: origin %s did not answer and no candidate did either, offered by %s; "+
					"the pod may still start from the node's cache",
				c.Name, c.Origin, strings.Join(c.Offering, ", "))
		}
	case attribution.Conceded:
		if afterLease {
			return EventRewriteConceded, corev1.EventTypeWarning, fmt.Sprintf(
				"Container %s: another mutating webhook replaced %s, placed by %s for origin %s, with %s",
				c.Name, c.Record.RewrittenTo, c.Record.By, c.Record.Origin, c.Image)
		}
	case attribution.Stale:
		return EventRewriteStale, corev1.EventTypeWarning, fmt.Sprintf(
			"Container %s: edited after admission to run %s, where %s had rewritten origin %s to %s; "+
				"roll the workload to send it back through admission",
			c.Name, c.Image, c.Record.By, c.Record.Origin, c.Record.RewrittenTo)
	case attribution.Untouched:
	}
	return "", "", ""
}

// publish sets the routing series of resource, and deletes the anomaly series that no longer
// hold. Called with t.mu held.
func (t *Tracker) publish(resource routing.Resource, status kuikv1alpha1.RoutingStatus, anomalies [4]map[imageLabels]map[types.UID]bool) {
	counts := map[attribution.State]int32{
		attribution.Untouched:      status.Containers.Untouched,
		attribution.Rewritten:      status.Containers.Rewritten,
		attribution.Conceded:       status.Containers.Conceded,
		attribution.Stale:          status.Containers.Stale,
		attribution.NoAlternatives: status.Containers.NoAlternatives,
	}
	for _, state := range attribution.States {
		t.containers.WithLabelValues(resource.Kind, resource.Name, string(state)).Set(float64(counts[state]))
	}
	t.podsTracked.WithLabelValues(resource.Kind, resource.Name).Set(float64(status.Pods.Tracked))
	t.podsRewritten.WithLabelValues(resource.Kind, resource.Name).Set(float64(status.Pods.Rewritten))

	previous := t.published[resource]
	var current [4]map[imageLabels]bool
	for i, series := range anomalies {
		current[i] = map[imageLabels]bool{}
		for labels, pods := range series {
			current[i][labels] = true
			t.anomalies[i].WithLabelValues(resource.Kind, resource.Name, labels.image, labels.registry).Set(float64(len(pods)))
		}
		for labels := range previous[i] {
			if !current[i][labels] {
				t.anomalies[i].DeleteLabelValues(resource.Kind, resource.Name, labels.image, labels.registry)
			}
		}
	}
	t.published[resource] = current
}

// Forget removes the series and the memory of a deleted resource.
func (t *Tracker) Forget(resource routing.Resource) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.announced, resource)
	delete(t.published, resource)
	labels := prometheus.Labels{kindLabel: resource.Kind, nameLabel: resource.Name}
	t.containers.DeletePartialMatch(labels)
	t.podsTracked.DeletePartialMatch(labels)
	t.podsRewritten.DeletePartialMatch(labels)
	for _, a := range t.anomalies {
		a.DeletePartialMatch(labels)
	}
}

// SetConditions sets FallbackActive and AlternativesExhausted on conditions from status,
// read before it is capped. kind words the message: an ImageMirror routes to the mirror.
func SetConditions(conditions *[]metav1.Condition, kind string, status kuikv1alpha1.RoutingStatus, generation int64) {
	var fallbackPods, exhaustedPods int32
	for _, e := range status.ActiveFallbacks {
		fallbackPods += e.Pods
	}
	for _, e := range status.NoAlternatives {
		exhaustedPods += e.Pods
	}
	condition.SetAnomaly(conditions, kuikv1alpha1.ConditionFallbackActive, len(status.ActiveFallbacks) > 0,
		kuikv1alpha1.ReasonOriginUnavailable,
		fmt.Sprintf("%s routed to %s (%s)", plural(len(status.ActiveFallbacks), "image"), destination(kind), plural(int(fallbackPods), "pod")),
		generation)
	condition.SetAnomaly(conditions, kuikv1alpha1.ConditionAlternativesExhausted, len(status.NoAlternatives) > 0,
		kuikv1alpha1.ReasonAllCandidatesFailed,
		fmt.Sprintf("%s unavailable (%s)", plural(len(status.NoAlternatives), "image"), plural(int(exhaustedPods), "pod")),
		generation)
}

// destination is where kind routes a fallback to, as the FallbackActive message names it.
func destination(kind string) string {
	if kind == routing.KindImageMirror {
		return "the mirror"
	}
	return "fallback"
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Cap caps the four anomaly lists of status in pass, under their status field names.
func Cap(pass *capped.Pass, status *kuikv1alpha1.RoutingStatus) {
	status.ActiveFallbacks = capped.Cap(pass, listActiveFallbacks, status.ActiveFallbacks,
		func(e kuikv1alpha1.ActiveFallback) metav1.Time { return e.Since }, fallbackKeyOf)
	status.NoAlternatives = capped.Cap(pass, listNoAlternatives, status.NoAlternatives,
		func(e kuikv1alpha1.NoAlternative) metav1.Time { return e.Since },
		func(e kuikv1alpha1.NoAlternative) string { return e.Image })
	status.ConcededRewrites = capped.Cap(pass, listConcededRewrites, status.ConcededRewrites,
		func(e kuikv1alpha1.ReplacedRewrite) metav1.Time { return e.Since }, keyOfReplaced)
	status.StaleRewrites = capped.Cap(pass, listStaleRewrites, status.StaleRewrites,
		func(e kuikv1alpha1.ReplacedRewrite) metav1.Time { return e.Since }, keyOfReplaced)
}
