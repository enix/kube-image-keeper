package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// FilterItem is a single include / exclude selector for the unified filter.
// Exactly one of its fields is set; the populated field determines the
// dimension (image, label or annotation) the item contributes to. The
// "exactly one field" contract is enforced per-item by the XValidation rules
// on the Include/Exclude lists that hold these items (the cluster-scoped lists
// add the namespace dimension to the same rule).
type FilterItem struct {
	// Image is a regular expression matched against the normalised image reference.
	// +kubebuilder:validation:MaxLength=128
	Image string `json:"image,omitempty"`
	// Label is a Kubernetes label-selector string matched against the Pod's labels
	// (k8s.io/apimachinery/pkg/labels syntax: "key=value", "key!=value", "key",
	// "!key", "key in (a,b)", "key notin (a,b)").
	// +kubebuilder:validation:MaxLength=512
	Label string `json:"label,omitempty"`
	// Annotation is a Kubernetes label-selector string matched against the Pod's annotations.
	// +kubebuilder:validation:MaxLength=512
	Annotation string `json:"annotation,omitempty"`
}

// ClusterFilterItem is a FilterItem extended with the namespace dimension,
// available only on cluster-scoped resources.
type ClusterFilterItem struct {
	FilterItem `json:",inline"`
	// Namespace is a regular expression matched against the Pod's namespace.
	// +kubebuilder:validation:MaxLength=128
	Namespace string `json:"namespace,omitempty"`
}

// Filter is the unified pod/image selector for namespaced resources.
//
// Matching semantics: items are grouped by dimension (all image items, all
// label items, ...). Within a dimension the items are OR'd; across dimensions
// they are AND'd. Any matching exclude item drops the candidate. A dimension
// with no include items matches everything in that dimension.
type Filter struct {
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(item, (has(item.image) ? 1 : 0) + (has(item.label) ? 1 : 0) + (has(item.annotation) ? 1 : 0) == 1)",message="each filter item must set exactly one of image, label or annotation"
	Include []FilterItem `json:"include,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(item, (has(item.image) ? 1 : 0) + (has(item.label) ? 1 : 0) + (has(item.annotation) ? 1 : 0) == 1)",message="each filter item must set exactly one of image, label or annotation"
	Exclude []FilterItem `json:"exclude,omitempty"`
}

// ClusterFilter is the cluster-scoped counterpart of Filter; its items add the
// namespace dimension. Matching semantics are identical to Filter, with the
// namespace dimension AND'd in alongside image, label and annotation.
type ClusterFilter struct {
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(item, (has(item.image) ? 1 : 0) + (has(item.label) ? 1 : 0) + (has(item.annotation) ? 1 : 0) + (has(item.namespace) ? 1 : 0) == 1)",message="each filter item must set exactly one of image, label, annotation or namespace"
	Include []ClusterFilterItem `json:"include,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(item, (has(item.image) ? 1 : 0) + (has(item.label) ? 1 : 0) + (has(item.annotation) ? 1 : 0) + (has(item.namespace) ? 1 : 0) == 1)",message="each filter item must set exactly one of image, label, annotation or namespace"
	Exclude []ClusterFilterItem `json:"exclude,omitempty"`
}

// IsEmpty reports whether the filter declares no items at all. An empty filter
// matches everything; callers use this to detect whether the filter mode is in
// use (precedence over the deprecated imageFilter).
func (f Filter) IsEmpty() bool { return len(f.Include)+len(f.Exclude) == 0 }

// IsEmpty reports whether the cluster filter declares no items at all.
func (cf ClusterFilter) IsEmpty() bool { return len(cf.Include)+len(cf.Exclude) == 0 }

// filterSelector is implemented by both Filter and ClusterFilter. It lets the
// per-kind accessors share the single precedence rule below instead of copying
// the "unified filter wins, otherwise fall back" branch into every accessor.
type filterSelector interface {
	IsEmpty() bool
	BuildPodMatcher() (func(pod *corev1.Pod) bool, error)
	BuildImageFilter() (filter.Filter, error)
}

// matchAllPods is the pod matcher used when spec.filter is unset: it matches
// every pod, the behaviour the removed podFilter/namespaceFilter fields had.
func matchAllPods(*corev1.Pod) bool { return true }

// podMatcher resolves a kind's pod matcher: the unified filter when set,
// otherwise match-all.
func podMatcher(f filterSelector) (func(pod *corev1.Pod) bool, error) {
	if f.IsEmpty() {
		return matchAllPods, nil
	}
	return f.BuildPodMatcher()
}

// imageFilter resolves a kind's image filter: the unified filter when set,
// otherwise the deprecated imageFilter supplied via legacy.
func imageFilter(f filterSelector, legacy func() (filter.Filter, error)) (filter.Filter, error) {
	if f.IsEmpty() {
		return legacy()
	}
	return f.BuildImageFilter()
}

// BuildImageFilter compiles the image dimension into an image filter. An empty
// image dimension matches every image.
func (f Filter) BuildImageFilter() (filter.Filter, error) {
	include := defaultedToMatchAll(collectStrings(f.Include, imageField))
	return filter.CompileIncludeExcludeFilter(include, collectStrings(f.Exclude, imageField))
}

// BuildPodMatcher compiles the label and annotation dimensions into a pod
// matcher. An empty dimension matches every pod for that dimension.
func (f Filter) BuildPodMatcher() (func(pod *corev1.Pod) bool, error) {
	podFilter, err := filter.CompilePodFilter(
		collectStrings(f.Include, labelField),
		collectStrings(f.Exclude, labelField),
		collectStrings(f.Include, annotationField),
		collectStrings(f.Exclude, annotationField),
	)
	if err != nil {
		return nil, err
	}
	return podFilter.Match, nil
}

// ToFilter drops the namespace dimension, projecting a ClusterFilter onto the
// namespaced Filter shape. Used when a cluster resource is flattened into its
// namespaced form after the namespace has already been matched.
func (cf ClusterFilter) ToFilter() Filter {
	f := Filter{
		Include: make([]FilterItem, len(cf.Include)),
		Exclude: make([]FilterItem, len(cf.Exclude)),
	}
	for i, item := range cf.Include {
		f.Include[i] = item.FilterItem
	}
	for i, item := range cf.Exclude {
		f.Exclude[i] = item.FilterItem
	}
	return f
}

// BuildImageFilter compiles the image dimension; identical to Filter.
func (cf ClusterFilter) BuildImageFilter() (filter.Filter, error) {
	return cf.ToFilter().BuildImageFilter()
}

// BuildPodMatcher compiles the label, annotation and namespace dimensions. The
// returned matcher requires the pod to satisfy the label/annotation dimensions
// AND its namespace to match the namespace dimension. An empty namespace
// dimension matches every namespace.
func (cf ClusterFilter) BuildPodMatcher() (func(pod *corev1.Pod) bool, error) {
	podMatch, err := cf.ToFilter().BuildPodMatcher()
	if err != nil {
		return nil, err
	}
	include := defaultedToMatchAll(collectStrings(cf.Include, namespaceField))
	nsFilter, err := filter.CompileIncludeExcludeFilter(include, collectStrings(cf.Exclude, namespaceField))
	if err != nil {
		return nil, err
	}
	return func(pod *corev1.Pod) bool {
		return podMatch(pod) && nsFilter.Match(pod.Namespace)
	}, nil
}

// field selectors used to group items by dimension.
func imageField(i FilterItem) string            { return i.Image }
func labelField(i FilterItem) string            { return i.Label }
func annotationField(i FilterItem) string       { return i.Annotation }
func namespaceField(i ClusterFilterItem) string { return i.Namespace }

// collectStrings returns the non-empty get(item) values, in declaration order.
func collectStrings[T any](items []T, get func(T) string) []string {
	var out []string
	for _, item := range items {
		if v := get(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// defaultedToMatchAll returns include unchanged, or a match-everything pattern
// ([".*"]) when it is empty. CompileIncludeExcludeFilter treats an empty include
// list as "match nothing", so callers that want "an empty dimension matches
// everything" must inject the wildcard.
func defaultedToMatchAll(include []string) []string {
	if len(include) == 0 {
		return []string{".*"}
	}
	return include
}
