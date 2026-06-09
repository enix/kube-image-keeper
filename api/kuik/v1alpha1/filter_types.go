package v1alpha1

import (
	"github.com/enix/kube-image-keeper/internal/filter"
	corev1 "k8s.io/api/core/v1"
)

// FilterItem is a single include / exclude selector for the unified filter.
// Exactly one of its fields is set; the populated field determines the
// dimension (image, label or annotation) the item contributes to.
type FilterItem struct {
	// Image is a regular expression matched against the normalised image reference.
	Image string `json:"image,omitempty"`
	// Label is a Kubernetes label-selector string matched against the Pod's labels
	// (k8s.io/apimachinery/pkg/labels syntax: "key=value", "key!=value", "key",
	// "!key", "key in (a,b)", "key notin (a,b)").
	Label string `json:"label,omitempty"`
	// Annotation is a Kubernetes label-selector string matched against the Pod's annotations.
	Annotation string `json:"annotation,omitempty"`
}

// ClusterFilterItem is a FilterItem extended with the namespace dimension,
// available only on cluster-scoped resources.
type ClusterFilterItem struct {
	FilterItem `json:",inline"`
	// Namespace is a regular expression matched against the Pod's namespace.
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
	Include []FilterItem `json:"include,omitempty"`
	// +optional
	Exclude []FilterItem `json:"exclude,omitempty"`
}

// ClusterFilter is the cluster-scoped counterpart of Filter; its items add the
// namespace dimension. Matching semantics are identical to Filter, with the
// namespace dimension AND'd in alongside image, label and annotation.
type ClusterFilter struct {
	// +optional
	Include []ClusterFilterItem `json:"include,omitempty"`
	// +optional
	Exclude []ClusterFilterItem `json:"exclude,omitempty"`
}

// IsEmpty reports whether the filter declares no items at all. An empty filter
// matches everything; callers use this to detect whether the filter mode is in
// use (precedence over the deprecated imageFilter).
func (f Filter) IsEmpty() bool { return len(f.Include)+len(f.Exclude) == 0 }

// IsEmpty reports whether the cluster filter declares no items at all.
func (cf ClusterFilter) IsEmpty() bool { return len(cf.Include)+len(cf.Exclude) == 0 }

// BuildImageFilter compiles the image dimension into an image matcher. An empty
// image dimension matches every image.
func (f Filter) BuildImageFilter() (filter.Filter, error) {
	include := collectFilterItems(f.Include, imageField)
	if len(include) == 0 {
		include = []string{".*"}
	}
	return filter.CompileIncludeExcludeFilter(include, collectFilterItems(f.Exclude, imageField))
}

// BuildPodMatcher compiles the label and annotation dimensions into a pod
// matcher. An empty dimension matches every pod for that dimension.
func (f Filter) BuildPodMatcher() (func(pod *corev1.Pod) bool, error) {
	podFilter, err := filter.CompilePodFilter(
		collectFilterItems(f.Include, labelField),
		collectFilterItems(f.Exclude, labelField),
		collectFilterItems(f.Include, annotationField),
		collectFilterItems(f.Exclude, annotationField),
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
	include := collectClusterNamespaces(cf.Include)
	if len(include) == 0 {
		include = []string{".*"}
	}
	nsFilter, err := filter.CompileIncludeExcludeFilter(include, collectClusterNamespaces(cf.Exclude))
	if err != nil {
		return nil, err
	}
	return func(pod *corev1.Pod) bool {
		return podMatch(pod) && nsFilter.Match(pod.Namespace)
	}, nil
}

// field selectors used to group items by dimension.
func imageField(i FilterItem) string      { return i.Image }
func labelField(i FilterItem) string      { return i.Label }
func annotationField(i FilterItem) string { return i.Annotation }

// collectFilterItems returns the non-empty values of one dimension, in order.
func collectFilterItems(items []FilterItem, get func(FilterItem) string) []string {
	var out []string
	for _, item := range items {
		if v := get(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// collectClusterNamespaces returns the non-empty namespace values, in order.
func collectClusterNamespaces(items []ClusterFilterItem) []string {
	var out []string
	for _, item := range items {
		if item.Namespace != "" {
			out = append(out, item.Namespace)
		}
	}
	return out
}
