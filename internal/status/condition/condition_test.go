package condition

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

const (
	kind = "ImageAlternative"
	name = "quay"
)

// event is one event the recorder received.
type event struct {
	regarding runtime.Object
	eventType string
	reason    string
	note      string
}

// recorder keeps the events it receives.
type recorder struct{ events []event }

func (r *recorder) Eventf(regarding, _ runtime.Object, eventType, reason, _, note string, args ...any) {
	r.events = append(r.events, event{regarding: regarding, eventType: eventType, reason: reason, note: fmt.Sprintf(note, args...)})
}

// value returns the value of the kuik_resource_not_ready series with the given labels, and
// whether it exists.
func value(registry *prometheus.Registry, labels map[string]string) (float64, bool) {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() != "kuik_resource_not_ready" {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue series
				}
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// seriesCount returns how many kuik_resource_not_ready series exist.
func seriesCount(registry *prometheus.Registry) int {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() == "kuik_resource_not_ready" {
			return len(f.GetMetric())
		}
	}
	return 0
}

var secretNotFound = &NotReady{Reason: kuikv1alpha1.ReasonSecretNotFound, Message: `secret "quay-creds" not found`}

var _ = Describe("Conditions", func() {
	Describe("SetAnomaly", func() {
		It("adds the condition True with its reason and message while the anomaly is active", func() {
			var conditions []metav1.Condition
			SetAnomaly(&conditions, kuikv1alpha1.ConditionFallbackActive, true, kuikv1alpha1.ReasonOriginUnavailable, "1 image routed to fallback (12 pods)", 3)
			c := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionFallbackActive)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(kuikv1alpha1.ReasonOriginUnavailable))
			Expect(c.Message).To(Equal("1 image routed to fallback (12 pods)"))
			Expect(c.ObservedGeneration).To(Equal(int64(3)))
		})

		It("removes the condition once the anomaly ends", func() {
			var conditions []metav1.Condition
			SetAnomaly(&conditions, kuikv1alpha1.ConditionFallbackActive, true, kuikv1alpha1.ReasonOriginUnavailable, "1 image routed to fallback (12 pods)", 1)
			SetAnomaly(&conditions, kuikv1alpha1.ConditionFallbackActive, false, kuikv1alpha1.ReasonOriginUnavailable, "", 1)
			Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionFallbackActive)).To(BeNil())
		})
	})

	Describe("Readiness", func() {
		var (
			registry   *prometheus.Registry
			events     *recorder
			readiness  *Readiness
			object     *kuikv1alpha1.ImageAlternative
			conditions []metav1.Condition
		)

		BeforeEach(func() {
			registry = prometheus.NewRegistry()
			events = &recorder{}
			var err error
			readiness, err = NewReadiness(events, registry)
			Expect(err).NotTo(HaveOccurred())
			object = &kuikv1alpha1.ImageAlternative{ObjectMeta: metav1.ObjectMeta{Name: name}}
			conditions = nil
		})

		set := func(notReady *NotReady) {
			readiness.Set(object, kind, name, &conditions, 1, notReady)
		}
		ready := func() *metav1.Condition {
			return meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionReady)
		}

		It("sets Ready True with reason IsReady when nothing is wrong", func() {
			set(nil)
			Expect(ready().Status).To(Equal(metav1.ConditionTrue))
			Expect(ready().Reason).To(Equal(kuikv1alpha1.ReasonIsReady))
		})

		It("sets Ready False with the reason and message it is given", func() {
			set(secretNotFound)
			Expect(ready().Status).To(Equal(metav1.ConditionFalse))
			Expect(ready().Reason).To(Equal(kuikv1alpha1.ReasonSecretNotFound))
			Expect(ready().Message).To(Equal(secretNotFound.Message))
		})

		It("emits a Warning ResourceNotReady carrying the reason when Ready flips to False", func() {
			set(nil)
			set(secretNotFound)
			Expect(events.events).To(HaveLen(1))
			Expect(events.events[0].regarding).To(BeIdenticalTo(object))
			Expect(events.events[0].eventType).To(Equal(corev1.EventTypeWarning))
			Expect(events.events[0].reason).To(Equal(EventResourceNotReady))
			Expect(events.events[0].note).To(ContainSubstring(kuikv1alpha1.ReasonSecretNotFound))
		})

		It("emits a Normal ResourceReady when Ready flips back to True", func() {
			set(secretNotFound)
			set(nil)
			Expect(events.events).To(HaveLen(2))
			Expect(events.events[1].eventType).To(Equal(corev1.EventTypeNormal))
			Expect(events.events[1].reason).To(Equal(EventResourceReady))
		})

		It("emits a ResourceNotReady when a resource with no Ready condition yet is not ready", func() {
			set(secretNotFound)
			Expect(events.events).To(HaveLen(1))
			Expect(events.events[0].reason).To(Equal(EventResourceNotReady))
		})

		It("emits nothing when a resource with no Ready condition yet is ready", func() {
			set(nil)
			Expect(events.events).To(BeEmpty())
		})

		It("emits nothing when the persisted Ready is already False, as after a restart", func() {
			conditions = []metav1.Condition{{
				Type: kuikv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
				Reason: kuikv1alpha1.ReasonSecretNotFound, LastTransitionTime: metav1.Now(),
			}}
			set(secretNotFound)
			Expect(events.events).To(BeEmpty())
		})

		It("announces a flip once when the next set reads the conditions as they were before the write", func() {
			persisted := []metav1.Condition{{
				Type: kuikv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
				Reason: kuikv1alpha1.ReasonIsReady, LastTransitionTime: metav1.Now(),
			}}
			// The write of the first set failed, or the cache does not show it yet: the next set
			// starts again from the conditions of before.
			for range 2 {
				conditions = append([]metav1.Condition(nil), persisted...)
				set(secretNotFound)
			}
			Expect(events.events).To(HaveLen(1))
		})

		It("emits nothing while Ready keeps its status", func() {
			set(nil)
			set(nil)
			set(secretNotFound)
			set(secretNotFound)
			Expect(events.events).To(HaveLen(1))
		})

		It("exports kuik_resource_not_ready at 1 with the reason while Ready is False", func() {
			set(secretNotFound)
			v, ok := value(registry, map[string]string{"kind": kind, "name": name, "reason": kuikv1alpha1.ReasonSecretNotFound})
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(1.0))
		})

		It("deletes the kuik_resource_not_ready series once Ready is True", func() {
			set(secretNotFound)
			set(nil)
			Expect(seriesCount(registry)).To(BeZero())
		})

		It("replaces the series when the reason changes, never keeping two", func() {
			set(secretNotFound)
			set(&NotReady{Reason: kuikv1alpha1.ReasonSecretMalformed, Message: "not a dockerconfigjson"})
			Expect(seriesCount(registry)).To(Equal(1))
			_, ok := value(registry, map[string]string{"reason": kuikv1alpha1.ReasonSecretMalformed})
			Expect(ok).To(BeTrue())
		})

		It("deletes the series of a forgotten resource", func() {
			set(secretNotFound)
			readiness.Forget(kind, name)
			Expect(seriesCount(registry)).To(BeZero())
		})
	})
})
