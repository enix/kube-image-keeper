package filter

import (
	"strings"
)

type PrefixIncludeExcludeFilter struct {
	IncludeExcludeFilter
	prefix string
}

func CompilePrefixIncludeExcludeFilter(prefix string, include, exclude []string) (*PrefixIncludeExcludeFilter, error) {
	filter, err := CompileIncludeExcludeFilter(include, exclude)
	if err != nil {
		return nil, err
	}
	return &PrefixIncludeExcludeFilter{
		IncludeExcludeFilter: *filter,
		prefix:               prefix,
	}, nil
}

func (p *PrefixIncludeExcludeFilter) Match(s string) bool {
	if after, found := strings.CutPrefix(s, p.prefix); !found {
		return false
	} else {
		return p.IncludeExcludeFilter.Match(after)
	}
}
