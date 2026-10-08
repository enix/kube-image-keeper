// Package capped caps the anomaly lists of a status. The truncation, the `truncated` map, the
// ListCapacityPressure condition and the three kuik_status_list_* series come out of this one
// place, so they cannot contradict each other. See docs/v3/status.md, "Bounded lists".
package capped

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

const (
	// Capacity is the number of entries every capped list holds at most, the same for every
	// list of every kind.
	Capacity = 500
	// NearCapacity is 80% of Capacity, from which ListCapacityPressure goes True.
	NearCapacity = Capacity * 8 / 10
)

// Limiter caps the lists of every resource of a controller, and remembers which entries it
// already counted as dropped.
type Limiter struct {
	entries, capacity *prometheus.GaugeVec
	dropped           *prometheus.CounterVec
	// limit is the cap of every list, Capacity unless an option set another.
	limit int

	mu sync.Mutex
	// out holds, per resource and list, the keys left out at the last write.
	out map[resource]map[string]map[string]bool
}

type resource struct{ kind, name string }

// The labels of the three series.
const (
	labelKind = "kind"
	labelName = "name"
	labelList = "list"
)

// Option configures a Limiter.
type Option func(*Limiter)

// WithCapacity caps every list at capacity instead of Capacity, ListCapacityPressure going
// True from 80% of it.
func WithCapacity(capacity int) Option {
	return func(l *Limiter) { l.limit = capacity }
}

// near is the number of entries from which ListCapacityPressure goes True: 80% of the cap.
func (l *Limiter) near() int {
	return l.limit * 8 / 10
}

// NewLimiter returns a limiter exporting the kuik_status_list_* series to registerer.
func NewLimiter(registerer prometheus.Registerer, opts ...Option) (*Limiter, error) {
	labels := []string{labelKind, labelName, labelList}
	l := &Limiter{
		entries: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_status_list_entries",
			Help: "Entries currently written in a capped status list",
		}, labels),
		capacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_status_list_capacity",
			Help: "The cap that list is subject to",
		}, labels),
		dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kuik_status_list_dropped_total",
			Help: "Entries a capped status list could not hold",
		}, labels),
		out: map[resource]map[string]map[string]bool{},
	}
	var err error
	if l.entries, err = register(registerer, l.entries); err != nil {
		return nil, err
	}
	if l.capacity, err = register(registerer, l.capacity); err != nil {
		return nil, err
	}
	if l.dropped, err = register(registerer, l.dropped); err != nil {
		return nil, err
	}
	l.limit = Capacity
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// register registers c, or returns the collector already registered under its name: each
// controller builds its own limiter on the same registry.
func register[C prometheus.Collector](registerer prometheus.Registerer, c C) (C, error) {
	err := registerer.Register(c)
	if are := (prometheus.AlreadyRegisteredError{}); errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(C); ok {
			return existing, nil
		}
	}
	return c, err
}

// Pass caps the lists of one status write of one resource.
type Pass struct {
	limiter  *Limiter
	resource resource
	lists    []capped
}

type capped struct {
	name    string
	written int
	out     map[string]bool
}

// Begin starts capping the lists of the resource (kind, name).
func (l *Limiter) Begin(kind, name string) *Pass {
	return &Pass{limiter: l, resource: resource{kind: kind, name: name}}
}

// Forget removes the series and the memory of a deleted resource.
func (l *Limiter) Forget(kind, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.out, resource{kind: kind, name: name})
	labels := prometheus.Labels{labelKind: kind, labelName: name}
	l.entries.DeletePartialMatch(labels)
	l.capacity.DeletePartialMatch(labels)
	l.dropped.DeletePartialMatch(labels)
}

// Cap returns at most Capacity entries of the list named list, the status field name, keeping
// the oldest by since and breaking ties by key. key identifies an entry from one write to the
// next, so a dropped entry is counted once.
func Cap[T any](p *Pass, list string, entries []T, since func(T) metav1.Time, key func(T) string) []T {
	sorted := slices.SortedStableFunc(slices.Values(entries), func(a, b T) int {
		if c := since(a).Compare(since(b).Time); c != 0 {
			return c
		}
		return cmp.Compare(key(a), key(b))
	})
	c := capped{name: list, out: map[string]bool{}}
	if limit := p.limiter.limit; len(sorted) > limit {
		for _, e := range sorted[limit:] {
			c.out[key(e)] = true
		}
		sorted = sorted[:limit]
	}
	c.written = len(sorted)
	p.lists = append(p.lists, c)
	return sorted
}

// End sets ListCapacityPressure on conditions from the lists capped in this pass, removing it
// when none is under pressure, publishes the series, and returns the `truncated` map: nil
// when nothing was left out.
func (p *Pass) End(conditions *[]metav1.Condition, generation int64) map[string]int32 {
	l := p.limiter
	l.mu.Lock()
	defer l.mu.Unlock()

	previous := l.out[p.resource]
	if previous == nil {
		previous = map[string]map[string]bool{}
		l.out[p.resource] = previous
	}

	var truncated map[string]int32
	var near, over []string
	for _, c := range p.lists {
		labels := prometheus.Labels{labelKind: p.resource.kind, labelName: p.resource.name, labelList: c.name}
		l.entries.With(labels).Set(float64(c.written))
		l.capacity.With(labels).Set(float64(l.limit))
		// Touch the counter so the series exists before anything is dropped.
		counter := l.dropped.With(labels)
		for k := range c.out {
			if !previous[c.name][k] {
				counter.Inc()
			}
		}
		previous[c.name] = c.out

		switch {
		case len(c.out) > 0:
			if truncated == nil {
				truncated = map[string]int32{}
			}
			truncated[c.name] = int32(len(c.out))
			over = append(over, c.name)
		case c.written >= l.near():
			near = append(near, c.name)
		}
	}

	switch {
	case len(over) > 0:
		meta.SetStatusCondition(conditions, pressure(kuikv1alpha1.ReasonListTruncated,
			fmt.Sprintf("entries left out of %s", strings.Join(over, ", ")), generation))
	case len(near) > 0:
		meta.SetStatusCondition(conditions, pressure(kuikv1alpha1.ReasonListNearCapacity,
			fmt.Sprintf("%s at %d%% of the cap of %d entries or more", strings.Join(near, ", "), l.near()*100/l.limit, l.limit), generation))
	default:
		meta.RemoveStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure)
	}
	return truncated
}

func pressure(reason, message string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               kuikv1alpha1.ConditionListCapacityPressure,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	}
}
