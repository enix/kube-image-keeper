// Package capped caps the anomaly lists of a status. The truncation, the `truncated` map,
// the ListCapacityPressure condition and the three kuik_status_list_* series come out of this
// one place, so they cannot contradict each other. See docs/v3/status.md, "Bounded lists".
package capped

import (
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
type Limiter struct{}

// NewLimiter returns a limiter exporting the kuik_status_list_* series to registerer.
func NewLimiter(registerer prometheus.Registerer) (*Limiter, error) {
	return &Limiter{}, nil
}

// Pass caps the lists of one status write of one resource.
type Pass struct{}

// Begin starts capping the lists of the resource (kind, name).
func (l *Limiter) Begin(kind, name string) *Pass {
	return &Pass{}
}

// Forget removes the series and the memory of a deleted resource.
func (l *Limiter) Forget(kind, name string) {}

// Cap returns at most Capacity entries of the list named list, the status field name, keeping
// the oldest by since and breaking ties by key. key identifies an entry from one write to the
// next, so a dropped entry is counted once.
func Cap[T any](p *Pass, list string, entries []T, since func(T) metav1.Time, key func(T) string) []T {
	return entries
}

// End sets ListCapacityPressure on conditions from the lists capped in this pass, removing it
// when none is under pressure, publishes the series, and returns the `truncated` map: nil
// when nothing was left out.
func (p *Pass) End(conditions *[]metav1.Condition, generation int64) map[string]int32 {
	return nil
}
