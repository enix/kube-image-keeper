package v1alpha1

import (
	"regexp"
)

func (r *Repository) CompileUpdateFilters() ([]regexp.Regexp, error) {
	regexps := make([]regexp.Regexp, len(r.Spec.UpdateFilters))

	for i, updateFilter := range r.Spec.UpdateFilters {
		r, err := regexp.Compile(updateFilter)
		if err != nil {
			return nil, err
		}
		regexps[i] = *r
	}

	return regexps, nil
}
