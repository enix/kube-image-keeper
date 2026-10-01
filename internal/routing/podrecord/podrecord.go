// Package podrecord reads and writes the three annotations the webhook records its decisions
// in, keyed by container name. See docs/v3/observability.md, "Annotations".
package podrecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
)

// The annotations, all JSON objects keyed by container name.
const (
	AnnotationRewrites         = "kuik.enix.io/rewrites"
	AnnotationConcededRewrites = "kuik.enix.io/conceded-rewrites"
	AnnotationNoAlternatives   = "kuik.enix.io/no-alternatives"
)

// Rewrite is one entry of kuik.enix.io/rewrites and kuik.enix.io/conceded-rewrites.
type Rewrite struct {
	// By is the resource that supplied the reference, as `<kind>/<name>`.
	By string `json:"by"`
	// Origin is the normalised reference the container came from.
	Origin string `json:"origin"`
	// RewrittenTo is the reference kuik placed.
	RewrittenTo string `json:"rewrittenTo"`
	// Policy is `Always` or `OnFailure`.
	Policy string `json:"policy"`
}

// Stands reports whether the rewrite still describes a container whose live image is live:
// kuik recognises its own output by comparing two strings. See docs/v3/architecture.md,
// "Recognising kuik's own output".
func (r Rewrite) Stands(live string) bool {
	return live == r.RewrittenTo
}

// Records are the three annotation maps of a pod. None is ever nil.
type Records struct {
	Rewrites       map[string]Rewrite
	Conceded       map[string]Rewrite
	NoAlternatives map[string][]string
}

// Read decodes the three annotation maps of pod. A map that does not decode reads as empty,
// and the error names its annotation.
func Read(pod *corev1.Pod) (Records, error) {
	r := Records{Rewrites: map[string]Rewrite{}, Conceded: map[string]Rewrite{}, NoAlternatives: map[string][]string{}}
	return r, errors.Join(
		decode(pod, AnnotationRewrites, &r.Rewrites),
		decode(pod, AnnotationConcededRewrites, &r.Conceded),
		decode(pod, AnnotationNoAlternatives, &r.NoAlternatives),
	)
}

func decode[V any](pod *corev1.Pod, annotation string, into *map[string]V) error {
	value, ok := pod.Annotations[annotation]
	if !ok {
		return nil
	}
	if err := json.Unmarshal([]byte(value), into); err != nil {
		*into = map[string]V{}
		return fmt.Errorf("decoding annotation %s: %w", annotation, err)
	}
	if *into == nil {
		// A literal `null` decodes to a nil map.
		*into = map[string]V{}
	}
	return nil
}

// Write writes back to pod the maps of r that differ from before, and removes an emptied one.
func (r Records) Write(pod *corev1.Pod, before Records) {
	encode(pod, AnnotationRewrites, before.Rewrites, r.Rewrites, maps.Equal)
	encode(pod, AnnotationConcededRewrites, before.Conceded, r.Conceded, maps.Equal)
	encode(pod, AnnotationNoAlternatives, before.NoAlternatives, r.NoAlternatives,
		func(x, y map[string][]string) bool { return maps.EqualFunc(x, y, slices.Equal) })
}

func encode[M ~map[string]V, V any](pod *corev1.Pod, annotation string, before, after M, equal func(M, M) bool) {
	if equal(before, after) {
		return
	}
	if len(after) == 0 {
		delete(pod.Annotations, annotation)
		return
	}
	// Maps of plain values: marshalling cannot fail.
	value, _ := json.Marshal(after)
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[annotation] = string(value)
}

// Standing returns the entries of kuik.enix.io/rewrites whose rewrite still stands on the
// live container, keyed by container name: a record gone stale, or naming a container the
// pod no longer has, is left out. A malformed annotation yields none.
func Standing(pod *corev1.Pod) map[string]Rewrite {
	var rewrites map[string]Rewrite
	if decode(pod, AnnotationRewrites, &rewrites) != nil {
		return map[string]Rewrite{}
	}
	standing := map[string]Rewrite{}
	for _, list := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for _, c := range list {
			if entry, ok := rewrites[c.Name]; ok && entry.Stands(c.Image) {
				standing[c.Name] = entry
			}
		}
	}
	return standing
}
