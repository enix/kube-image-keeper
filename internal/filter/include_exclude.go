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
		r, err := regexp.Compile("^(" + include[i] + ")$")
		if err != nil {
			return nil, err
		}
		filter.include[i] = *r
	}

	for i := range exclude {
		r, err := regexp.Compile("^(" + exclude[i] + ")$")
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
		return include.MatchString(s)
	})

	if included {
		return !slices.ContainsFunc(i.exclude, func(exclude regexp.Regexp) bool {
			return exclude.MatchString(s)
		})
	}

	return false
}

// NamespaceFilter matches namespace names with "include wins, default-allow"
// semantics: when both lists are empty every namespace is in scope; a
// namespace matching an include entry is always in scope (even if it also
// matches an exclude entry); a non-empty include list otherwise narrows the
// scope to its matches.
type NamespaceFilter struct {
	include []regexp.Regexp
	exclude []regexp.Regexp
}

func CompileNamespaceFilter(include, exclude []string) (*NamespaceFilter, error) {
	filter := &NamespaceFilter{
		include: make([]regexp.Regexp, len(include)),
		exclude: make([]regexp.Regexp, len(exclude)),
	}

	for i := range include {
		r, err := regexp.Compile("^(" + include[i] + ")$")
		if err != nil {
			return nil, err
		}
		filter.include[i] = *r
	}

	for i := range exclude {
		r, err := regexp.Compile("^(" + exclude[i] + ")$")
		if err != nil {
			return nil, err
		}
		filter.exclude[i] = *r
	}

	return filter, nil
}

func (f *NamespaceFilter) Match(s string) bool {
	if slices.ContainsFunc(f.include, func(r regexp.Regexp) bool {
		return r.MatchString(s)
	}) {
		return true
	}
	if len(f.include) > 0 {
		return false
	}
	return !slices.ContainsFunc(f.exclude, func(r regexp.Regexp) bool {
		return r.MatchString(s)
	})
}
