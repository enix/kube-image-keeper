// Package condition sets the conditions of a status. Ready is the only one that is True when
// things are well and is always present; every other one names an anomaly, is True while it
// lasts and is removed when it ends. See docs/v3/status.md, "Conditions and their reasons".
package condition

import (
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
)

// The events Ready emits when it flips. See docs/v3/observability.md, "Catalogue".
const (
	EventResourceReady    = "ResourceReady"
	EventResourceNotReady = "ResourceNotReady"
)

// SetAnomaly sets the anomaly condition conditionType True with reason and message while
// active, and removes it otherwise.
func SetAnomaly(conditions *[]metav1.Condition, conditionType string, active bool, reason, message string, generation int64) {
}

// NotReady is why a resource cannot work as declared: one of the resource reasons of
// docs/v3/status.md, "Conditions and their reasons".
type NotReady struct {
	Reason  string
	Message string
}

// Readiness sets the Ready condition of the resources of a controller, emits ResourceReady and
// ResourceNotReady when it flips, and exports kuik_resource_not_ready.
type Readiness struct{}

// NewReadiness returns a Readiness emitting with recorder and exporting to registerer.
func NewReadiness(recorder events.EventRecorder, registerer prometheus.Registerer) (*Readiness, error) {
	return &Readiness{}, nil
}

// Set sets Ready on conditions: True with reason IsReady when notReady is nil, False with its
// reason otherwise. object is the resource the events go on, kind its kind for the series.
func (r *Readiness) Set(object runtime.Object, kind, name string, conditions *[]metav1.Condition, generation int64, notReady *NotReady) {
}

// Forget removes the series of a deleted resource.
func (r *Readiness) Forget(kind, name string) {}
