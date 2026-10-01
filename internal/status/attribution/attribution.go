// Package attribution reads off a pod what kuik did to each of its containers, and which
// resource answers for it. See docs/v3/status.md, "Attribution".
package attribution

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/enix/kube-image-keeper/internal/routing"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

// State is what happened to a container. The values are the field names of the
// `containers` block of a status, and the `state` label of kuik_routing_containers.
type State string

const (
	// Untouched is a container no annotation names: the original answered.
	Untouched State = "untouched"
	// Rewritten is a container named by kuik.enix.io/rewrites whose rewrite still stands.
	Rewritten State = "rewritten"
	// Conceded is a container named by kuik.enix.io/conceded-rewrites.
	Conceded State = "conceded"
	// Stale is a container named by kuik.enix.io/rewrites whose live image no longer matches
	// rewrittenTo, the pod having been edited after admission.
	Stale State = "stale"
	// NoAlternatives is a container named by kuik.enix.io/no-alternatives.
	NoAlternatives State = "noAlternatives"
)

// States lists every state, in the order of the `containers` block.
var States = []State{Untouched, Rewritten, Conceded, Stale, NoAlternatives}

// Container is one routed container of a pod: an initContainer or a container, never an
// ephemeralContainer.
type Container struct {
	// Name is the container name. Its state compares the live image to rewrittenTo as
	// written, never normalised: kuik placed that exact string.
	Name string
	// Image is the live reference, normalised; the raw value when it does not parse.
	Image string
	// Origin is the reference the container's spec declares, except where a standing kuik
	// rewrite put Image there: then the rewrite's origin.
	Origin string
	// State is what happened to the container, whichever resource did it.
	State State
	// Record is the entry naming the container, for Rewritten, Conceded and Stale.
	Record *podrecord.Rewrite
	// Offering are the resources that offered a candidate, as `<kind>/<name>`, for
	// NoAlternatives.
	Offering []string
}

// StateFor is the state of c as resource counts it: Untouched unless the container is
// attributed to resource. Rewritten, Conceded and Stale are attributed to the resource of
// their record, NoAlternatives to every resource that offered a candidate.
func (c Container) StateFor(resource routing.Resource) State {
	return ""
}

// Live reports whether pod is non-terminal, Pending or Running: the only pods attribution
// counts.
func Live(pod *corev1.Pod) bool {
	return false
}

// Static reports whether pod is the mirror pod of a static pod, which the webhook never
// routes: the routing side of a status and the mirror's desired state leave it out, an
// ImageMonitor still tracks its images. See docs/v3/spec.md, "What the webhook never rewrites".
func Static(pod *corev1.Pod) bool {
	return false
}

// Containers returns the routed containers of pod, initContainers included. A malformed
// annotation reads as empty, its containers then counting as untouched.
func Containers(pod *corev1.Pod) []Container {
	return nil
}
