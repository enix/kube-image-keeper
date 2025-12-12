package filter

import (
	"regexp"
	"slices"
)

type IncludeExcludeFilter struct {
	include []regexp.Regexp
	exclude []regexp.Regexp
}

func CompileIncludeExcludeFilter(include, exclude []string) (*IncludeExcludeFilter, error) {
	filter := &IncludeExcludeFilter{
		include: make([]regexp.Regexp, len(include)),
		exclude: make([]regexp.Regexp, len(exclude)),
	}

	for i := range include {
		r, err := regexp.Compile(include[i])
		if err != nil {
			return nil, err
		}
		filter.include[i] = *r
	}

	for i := range exclude {
		r, err := regexp.Compile(exclude[i])
		if err != nil {
			return nil, err
		}
		filter.exclude[i] = *r
	}

	if len(filter.include) == 0 && len(filter.exclude) > 0 {
		filter.include = []regexp.Regexp{*regexp.MustCompile(".*")}
	}

	return filter, nil
}

func (i *IncludeExcludeFilter) Match(s string) bool {
	included := slices.ContainsFunc(i.include, func(include regexp.Regexp) bool {
		return include.FindString(s) == s // Must be a full match
	})

	if included {
		return !slices.ContainsFunc(i.exclude, func(exclude regexp.Regexp) bool {
			return exclude.FindString(s) == s // Must be a full match
		})
	}

	return false
}
