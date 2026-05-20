package filter

import (
	"strings"
)

type PrefixFilter struct {
	Filter
	prefix string
}

func AddPrefixToFilter(prefix string, builder func() (Filter, error)) (*PrefixFilter, error) {
	filter, err := builder()
	if err != nil {
		return nil, err
	}
	return &PrefixFilter{
		Filter: filter,
		prefix: prefix,
	}, nil
}

func (p *PrefixFilter) Match(s string) bool {
	if after, found := strings.CutPrefix(s, p.prefix); !found {
		return false
	} else {
		return p.Filter.Match(after)
	}
}
