// Package condition sets the conditions of a status. Ready is the only one that is True when
// things are well and is always present; every other one names an anomaly, is True while it
// lasts and is removed when it ends. See docs/v3/status.md, "Conditions and their reasons".
package condition

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// The events Ready emits when it flips. See docs/v3/observability.md, "Catalogue".
const (
	EventResourceReady    = "ResourceReady"
	EventResourceNotReady = "ResourceNotReady"
)

// SetAnomaly sets the anomaly condition conditionType True with reason and message while
// active, and removes it otherwise.
func SetAnomaly(conditions *[]metav1.Condition, conditionType string, active bool, reason, message string, generation int64) {
	if !active {
		meta.RemoveStatusCondition(conditions, conditionType)
		return
	}
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// The labels of kuik_resource_not_ready.
const (
	labelKind   = "kind"
	labelName   = "name"
	labelReason = "reason"
)

// NotReady is why a resource cannot work as declared: one of the resource reasons of
// docs/v3/status.md, "Conditions and their reasons".
type NotReady struct {
	Reason  string
	Message string
}

// Readiness sets the Ready condition of the resources of a controller, emits ResourceReady and
// ResourceNotReady when it flips, and exports kuik_resource_not_ready.
type Readiness struct {
	recorder events.EventRecorder
	notReady *prometheus.GaugeVec
}

// NewReadiness returns a Readiness emitting with recorder and exporting to registerer.
func NewReadiness(recorder events.EventRecorder, registerer prometheus.Registerer) (*Readiness, error) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kuik_resource_not_ready",
		Help: "1 while a resource's Ready condition is False, carrying that condition's reason",
	}, []string{labelKind, labelName, labelReason})
	if err := registerer.Register(gauge); err != nil {
		are := prometheus.AlreadyRegisteredError{}
		if !errors.As(err, &are) {
			return nil, err
		}
		existing, ok := are.ExistingCollector.(*prometheus.GaugeVec)
		if !ok {
			return nil, err
		}
		// Each controller builds its own Readiness on the same registry.
		gauge = existing
	}
	return &Readiness{recorder: recorder, notReady: gauge}, nil
}

// Set sets Ready on conditions: True with reason IsReady when notReady is nil, False with its
// reason otherwise. object is the resource the events go on, kind its kind for the series.
//
// An event follows a flip of the persisted condition only, so a restart, which reads back the
// status as written, announces nothing.
func (r *Readiness) Set(object runtime.Object, kind, name string, conditions *[]metav1.Condition, generation int64, notReady *NotReady) {
	previous := meta.FindStatusCondition(*conditions, kuikv1alpha1.ConditionReady)
	wasReady := previous == nil || previous.Status == metav1.ConditionTrue

	ready := metav1.Condition{
		Type:               kuikv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             kuikv1alpha1.ReasonIsReady,
		ObservedGeneration: generation,
	}
	if notReady != nil {
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, notReady.Reason, notReady.Message
	}
	meta.SetStatusCondition(conditions, ready)

	r.notReady.DeletePartialMatch(prometheus.Labels{labelKind: kind, labelName: name})
	switch {
	case notReady != nil:
		r.notReady.WithLabelValues(kind, name, notReady.Reason).Set(1)
		if wasReady {
			r.recorder.Eventf(object, nil, corev1.EventTypeWarning, EventResourceNotReady, "Reconcile",
				"%s is not ready: %s: %s", name, notReady.Reason, notReady.Message)
		}
	case !wasReady:
		r.recorder.Eventf(object, nil, corev1.EventTypeNormal, EventResourceReady, "Reconcile", "%s is ready again", name)
	}
}

// Forget removes the series of a deleted resource.
func (r *Readiness) Forget(kind, name string) {
	r.notReady.DeletePartialMatch(prometheus.Labels{labelKind: kind, labelName: name})
}
