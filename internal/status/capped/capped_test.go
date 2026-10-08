package capped

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

const (
	kind = "ImageAlternative"
	name = "quay"
	list = "noAlternatives"
)

type entry struct {
	key   string
	since metav1.Time
}

var epoch = time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC)

// entries returns n entries, entry i stamped i minutes after epoch.
func entries(n int) []entry {
	list := make([]entry, 0, n)
	for i := range n {
		list = append(list, entry{key: fmt.Sprintf("e%04d", i), since: metav1.NewTime(epoch.Add(time.Duration(i) * time.Minute))})
	}
	return list
}

func since(e entry) metav1.Time { return e.since }
func key(e entry) string        { return e.key }

func keys(list []entry) []string {
	k := make([]string, 0, len(list))
	for _, e := range list {
		k = append(k, e.key)
	}
	return k
}

// write caps entries for (k, name) in one pass and returns the kept entries, the truncated
// map and the conditions.
func write(l *Limiter, k string, entries []entry) ([]entry, map[string]int32, []metav1.Condition) {
	var conditions []metav1.Condition
	p := l.Begin(k, name)
	kept := Cap(p, list, entries, since, key)
	truncated := p.End(&conditions, 1)
	return kept, truncated, conditions
}

// value returns the value of the series name with the given labels, and whether it exists.
func value(registry *prometheus.Registry, metric string, labels map[string]string) (float64, bool) {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() != metric {
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
			if m.GetCounter() != nil {
				return m.GetCounter().GetValue(), true
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

var series = map[string]string{"kind": kind, "name": name, "list": list}

var _ = Describe("Bounded lists", func() {
	var (
		registry *prometheus.Registry
		limiter  *Limiter
	)

	BeforeEach(func() {
		registry = prometheus.NewRegistry()
		var err error
		limiter, err = NewLimiter(registry)
		Expect(err).NotTo(HaveOccurred())
	})

	It("caps the lists at the capacity it was built with, ListCapacityPressure following 80% of it", func() {
		small, err := NewLimiter(prometheus.NewRegistry(), WithCapacity(5))
		Expect(err).NotTo(HaveOccurred())

		kept, truncated, conditions := write(small, kind, entries(6))
		Expect(kept).To(HaveLen(5))
		Expect(truncated).To(Equal(map[string]int32{list: 1}))
		Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure).Reason).To(Equal(kuikv1alpha1.ReasonListTruncated))

		_, _, conditions = write(small, kind, entries(4))
		Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure).Reason).To(Equal(kuikv1alpha1.ReasonListNearCapacity))
	})

	Describe("Cap", func() {
		It("keeps a list under the cap whole", func() {
			kept, _, _ := write(limiter, kind, entries(Capacity))
			Expect(kept).To(HaveLen(Capacity))
		})

		It("keeps the oldest entries by since once a list exceeds the cap", func() {
			all := entries(Capacity + 3)
			// Shuffled input: the oldest are kept whatever the order they come in.
			reversed := make([]entry, len(all))
			for i, e := range all {
				reversed[len(all)-1-i] = e
			}
			kept, _, _ := write(limiter, kind, reversed)
			Expect(keys(kept)).To(ConsistOf(keys(all[:Capacity])))
		})

		It("breaks ties on since by key, so the kept entries do not churn between writes", func() {
			all := entries(Capacity + 1)
			for i := range all {
				all[i].since = metav1.NewTime(epoch)
			}
			first, _, _ := write(limiter, kind, all)
			reversed := make([]entry, len(all))
			for i, e := range all {
				reversed[len(all)-1-i] = e
			}
			second, _, _ := write(limiter, kind, reversed)
			Expect(keys(second)).To(ConsistOf(keys(first)))
			Expect(keys(first)).NotTo(ContainElement(all[Capacity].key))
		})
	})

	Describe("the truncated map", func() {
		It("is absent when no list reached the cap", func() {
			_, truncated, _ := write(limiter, kind, entries(Capacity))
			Expect(truncated).To(BeNil())
		})

		It("records, per list, how many entries were left out", func() {
			_, truncated, _ := write(limiter, kind, entries(Capacity+12))
			Expect(truncated).To(Equal(map[string]int32{list: 12}))
		})

		It("drops a list that came back under the cap", func() {
			write(limiter, kind, entries(Capacity+12))
			_, truncated, _ := write(limiter, kind, entries(10))
			Expect(truncated).To(BeNil())
		})
	})

	Describe("ListCapacityPressure", func() {
		It("is absent while every list stays under 80% of the cap", func() {
			_, _, conditions := write(limiter, kind, entries(NearCapacity-1))
			Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure)).To(BeNil())
		})

		It("is True with reason ListNearCapacity from 80% of the cap", func() {
			_, _, conditions := write(limiter, kind, entries(NearCapacity))
			c := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(kuikv1alpha1.ReasonListNearCapacity))
		})

		It("is True with reason ListTruncated once a list leaves entries out", func() {
			_, _, conditions := write(limiter, kind, entries(Capacity+1))
			c := meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(kuikv1alpha1.ReasonListTruncated))
		})

		It("is removed once every list is back under 80% of the cap", func() {
			var conditions []metav1.Condition
			p := limiter.Begin(kind, name)
			Cap(p, list, entries(Capacity+1), since, key)
			p.End(&conditions, 1)
			p = limiter.Begin(kind, name)
			Cap(p, list, entries(10), since, key)
			p.End(&conditions, 1)
			Expect(meta.FindStatusCondition(conditions, kuikv1alpha1.ConditionListCapacityPressure)).To(BeNil())
		})
	})

	Describe("the kuik_status_list_* series", func() {
		It("sets kuik_status_list_entries to the entries written, per list", func() {
			write(limiter, kind, entries(Capacity+7))
			v, ok := value(registry, "kuik_status_list_entries", series)
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(float64(Capacity)))
		})

		It("counts an entry in kuik_status_list_dropped_total the first time it is left out", func() {
			write(limiter, kind, entries(Capacity+2))
			v, _ := value(registry, "kuik_status_list_dropped_total", series)
			Expect(v).To(Equal(2.0))
		})

		It("does not count again an entry still left out at the next write", func() {
			write(limiter, kind, entries(Capacity+2))
			write(limiter, kind, entries(Capacity+2))
			v, _ := value(registry, "kuik_status_list_dropped_total", series)
			Expect(v).To(Equal(2.0))
		})

		It("counts again an entry that came back and is left out anew", func() {
			write(limiter, kind, entries(Capacity+1))
			// The oldest is gone, so the newest fits again.
			kept, _, _ := write(limiter, kind, entries(Capacity + 1)[1:])
			Expect(keys(kept)).To(ContainElement(entries(Capacity + 1)[Capacity].key))
			write(limiter, kind, entries(Capacity+1))
			v, _ := value(registry, "kuik_status_list_dropped_total", series)
			Expect(v).To(Equal(2.0))
		})

		It("keeps the counts of two resources of different kinds sharing a name apart", func() {
			write(limiter, kind, entries(Capacity+1))
			write(limiter, "ImageMirror", entries(Capacity+3))
			alternative, _ := value(registry, "kuik_status_list_dropped_total", series)
			mirror, _ := value(registry, "kuik_status_list_dropped_total", map[string]string{"kind": "ImageMirror", "name": name, "list": list})
			Expect(alternative).To(Equal(1.0))
			Expect(mirror).To(Equal(3.0))
		})

		It("removes the series of a forgotten resource", func() {
			write(limiter, kind, entries(Capacity+1))
			limiter.Forget(kind, name)
			for _, metric := range []string{"kuik_status_list_entries", "kuik_status_list_capacity", "kuik_status_list_dropped_total"} {
				_, ok := value(registry, metric, series)
				Expect(ok).To(BeFalse(), metric)
			}
		})
	})
})
