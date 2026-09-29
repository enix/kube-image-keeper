// Package routing builds the ordered candidate list of a container image from the
// ImageAlternative and ImageMirror resources that apply to its pod. See docs/v3/spec.md,
// "Candidate ordering".
package routing

import (
	corev1 "k8s.io/api/core/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

// The kinds of the routing resources, as `by` and the metric labels name them.
const (
	KindImageAlternative = "ImageAlternative"
	KindImageMirror      = "ImageMirror"
)

// Resource identifies a routing resource.
type Resource struct {
	Kind string
	Name string
}

// String renders the resource as `<kind>/<name>`.
func (r Resource) String() string {
	return r.Kind + "/" + r.Name
}

// Config is what a candidate is probed and pulled with.
type Config struct {
	// Auth is the entry's `auth`, or the mirror's `destination.pull.auth`. Nil for none.
	Auth *kuikv1alpha1.Auth
	// Insecure reaches the registry over plain HTTP.
	Insecure bool
}

// Candidate is one reference a container may be routed to.
type Candidate struct {
	// Reference is the image reference to place in the container.
	Reference string
	// Config is what the reference is probed and pulled with.
	Config Config
	// Resource is the resource that offered it, nil for the original.
	Resource *Resource
	// Policy is the policy of the band the candidate sits in, empty for the original.
	Policy kuikv1alpha1.RewritePolicy
}

// Request is the container a candidate list is built for.
type Request struct {
	// Image is the container's normalised reference.
	Image imagepath.Reference
	// PullPolicy is the container's imagePullPolicy.
	PullPolicy corev1.PullPolicy
	// PodLabels are the labels of the pod.
	PodLabels map[string]string
	// NamespaceLabels are the labels of the pod's namespace.
	NamespaceLabels map[string]string
}

// Options are the global config settings the candidate list depends on.
type Options struct {
	// ClusterID suffixes the tag of a mirror candidate.
	ClusterID string
	// DemoteMirrorWithPullPolicyAlways ignores an ImageMirror's `rewritePolicy: Always` for a
	// container with `imagePullPolicy: Always`.
	DemoteMirrorWithPullPolicyAlways bool
}

// Result is the candidate list of a container.
type Result struct {
	// Candidates are the references to probe, in order, the original included.
	Candidates []Candidate
	// Offering are the resources that offered at least one candidate.
	Offering []Resource
}

// Index holds the routing resources and answers candidate lists from them. It is built
// whole and never modified: a change of the resources builds a new one.
type Index struct{}

// NewIndex builds an index over the given resources.
func NewIndex(alternatives []kuikv1alpha1.ImageAlternative, mirrors []kuikv1alpha1.ImageMirror) *Index {
	return &Index{}
}

// Candidates returns the candidate list of a container.
func (i *Index) Candidates(req Request, opts Options) Result {
	return Result{}
}
