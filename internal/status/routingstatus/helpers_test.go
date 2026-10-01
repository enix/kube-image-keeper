package routingstatus

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

var (
	// self is the resource reported on, other one that selects the same pods.
	self  = routing.Resource{Kind: routing.KindImageAlternative, Name: "quay"}
	other = routing.Resource{Kind: routing.KindImageAlternative, Name: "other"}

	// elected is when the lease was acquired; pods are created after it unless a spec says
	// otherwise, and reported on at now.
	elected = time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC)
	created = elected.Add(time.Hour)
	now     = metav1.NewTime(elected.Add(2 * time.Hour))
)

const (
	thanos        = "quay.io/thanos/thanos:v0.42.2"
	ghcrThanos    = "ghcr.io/thanos-io/thanos:v0.42.2"
	ecrThanos     = "public.ecr.aws/thanos/thanos:v0.42.2"
	thanosNext    = "quay.io/thanos/thanos:v0.43.0"
	proxy         = "quay.io/oauth2-proxy/oauth2-proxy:v7.7.1"
	ghcrProxy     = "ghcr.io/oauth2-proxy/oauth2-proxy:v7.7.1"
	internalPxy   = "internal.tld/oauth2-proxy:v7.7.1"
	reloader      = "quay.io/prometheus-operator/prometheus-config-reloader:v0.80.0"
	nginx         = "docker.io/library/nginx:1.27"
	onFailure     = "OnFailure"
	always        = "Always"
	nsDefault     = "default"
	containerApp  = "app"
	labelImage    = "image"
	quay          = "quay.io"
	labelRegistry = "registry"
)

// spec is one container of a pod under construction and what the webhook recorded for it.
type spec struct {
	name, image string
	rewrite     *podrecord.Rewrite
	conceded    *podrecord.Rewrite
	offering    []string
}

func rewrite(by routing.Resource, origin, to, policy string) *podrecord.Rewrite {
	return &podrecord.Rewrite{By: by.String(), Origin: origin, RewrittenTo: to, Policy: policy}
}

// rewritten is a container by rewrote from origin to to, still running to.
func rewritten(name string, by routing.Resource, origin, to, policy string) spec {
	return spec{name: name, image: to, rewrite: rewrite(by, origin, to, policy)}
}

// stale is a container by rewrote from thanos to its ghcr.io copy, edited since to run live.
func stale(name string, by routing.Resource, live string) spec {
	return spec{name: name, image: live, rewrite: rewrite(by, thanos, ghcrThanos, onFailure)}
}

// conceded is a container by rewrote from origin to to, that another webhook set to live.
func conceded(name string, by routing.Resource, origin, to, live string) spec {
	return spec{name: name, image: live, conceded: rewrite(by, origin, to, onFailure)}
}

// exhausted is a container no candidate served, offered by the given resources.
func exhausted(name, origin string, offering ...routing.Resource) spec {
	s := spec{name: name, image: origin}
	for _, r := range offering {
		s.offering = append(s.offering, r.String())
	}
	return s
}

// untouched is a container running nginx that no annotation names.
func untouched(name string) spec {
	return spec{name: name, image: nginx}
}

// pod builds a Running pod created at created, with the annotations its containers need.
func pod(name string, containers ...spec) *corev1.Pod {
	rewrites := map[string]podrecord.Rewrite{}
	concededs := map[string]podrecord.Rewrite{}
	noAlternatives := map[string][]string{}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: nsDefault, UID: types.UID(name),
			CreationTimestamp: metav1.NewTime(created), Annotations: map[string]string{},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	for _, c := range containers {
		p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: c.name, Image: c.image})
		switch {
		case c.rewrite != nil:
			rewrites[c.name] = *c.rewrite
		case c.conceded != nil:
			concededs[c.name] = *c.conceded
		case c.offering != nil:
			noAlternatives[c.name] = c.offering
		}
	}
	annotate(p, podrecord.AnnotationRewrites, rewrites)
	annotate(p, podrecord.AnnotationConcededRewrites, concededs)
	annotate(p, podrecord.AnnotationNoAlternatives, noAlternatives)
	return p
}

func annotate[V any](p *corev1.Pod, annotation string, m map[string]V) {
	if len(m) == 0 {
		return
	}
	value, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	p.Annotations[annotation] = string(value)
}

func pods(list ...*corev1.Pod) []*corev1.Pod { return list }

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

// withReason returns the events of reason.
func (r *recorder) withReason(reason string) []event {
	var list []event
	for _, e := range r.events {
		if e.reason == reason {
			list = append(list, e)
		}
	}
	return list
}

// regarding returns the names of the pods the events went on.
func regarding(events []event) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.regarding.(*corev1.Pod).Name)
	}
	return names
}

// metrics reads the series of a registry.
type metrics struct{ registry *prometheus.Registry }

// value returns the value of the series metric with at least the given labels, and whether
// it exists.
func (m metrics) value(metric string, labels map[string]string) (float64, bool) {
	for _, s := range m.series(metric) {
		if matches(s.labels, labels) {
			return s.value, true
		}
	}
	return 0, false
}

// count returns how many series of metric carry at least the given labels.
func (m metrics) count(metric string, labels map[string]string) int {
	n := 0
	for _, s := range m.series(metric) {
		if matches(s.labels, labels) {
			n++
		}
	}
	return n
}

type sample struct {
	labels map[string]string
	value  float64
}

func (m metrics) series(metric string) []sample {
	families, err := m.registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	var samples []sample
	for _, f := range families {
		if f.GetName() != metric {
			continue
		}
		for _, s := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range s.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			v := s.GetGauge().GetValue()
			samples = append(samples, sample{labels: labels, value: v})
		}
	}
	return samples
}

func matches(got, want map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// of is the labels of a series of self.
func of(labels map[string]string) map[string]string {
	all := map[string]string{"kind": self.Kind, "name": self.Name}
	maps.Copy(all, labels)
	return all
}
