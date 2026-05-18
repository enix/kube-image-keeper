package filter

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// PodFilter matches pods against label and annotation selectors with
// standard "include AND NOT exclude" semantics. Each entry is a Kubernetes
// selector string parsed by k8s.io/apimachinery/pkg/labels.Parse, accepting
// equality form ("key=value"), inequality ("key!=value"), presence ("key"),
// absence ("!key") and set-based operators ("key in (a,b)", "key notin (a,b)").
// A pod is in scope when, independently for labels and annotations:
//   - some Include selector matches (or Include is empty), AND
//   - no Exclude selector matches.
type PodFilter struct {
	includeLabels      []labels.Selector
	excludeLabels      []labels.Selector
	includeAnnotations []labels.Selector
	excludeAnnotations []labels.Selector
}

func CompilePodFilter(includeLabels, excludeLabels, includeAnnotations, excludeAnnotations []string) (*PodFilter, error) {
	var (
		f   PodFilter
		err error
	)
	if f.includeLabels, err = parseSelectors(includeLabels); err != nil {
		return nil, err
	}
	if f.excludeLabels, err = parseSelectors(excludeLabels); err != nil {
		return nil, err
	}
	if f.includeAnnotations, err = parseSelectors(includeAnnotations); err != nil {
		return nil, err
	}
	if f.excludeAnnotations, err = parseSelectors(excludeAnnotations); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *PodFilter) Match(pod *corev1.Pod) bool {
	if !matchSelectorSets(f.includeLabels, f.excludeLabels, labels.Set(pod.Labels)) {
		return false
	}
	return matchSelectorSets(f.includeAnnotations, f.excludeAnnotations, labels.Set(pod.Annotations))
}

func parseSelectors(entries []string) ([]labels.Selector, error) {
	out := make([]labels.Selector, len(entries))
	for i, e := range entries {
		sel, err := labels.Parse(e)
		if err != nil {
			return nil, err
		}
		out[i] = sel
	}
	return out, nil
}

func matchSelectorSets(include, exclude []labels.Selector, set labels.Set) bool {
	if len(include) > 0 {
		matched := false
		for _, s := range include {
			if s.Matches(set) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, s := range exclude {
		if s.Matches(set) {
			return false
		}
	}
	return true
}
