package routing

import (
	"regexp"
	"strings"
)

type Strategy struct {
	Paths      []*regexp.Regexp
	Registries []string
}

func (s *Strategy) Match(reference string) bool {
	if match(reference, s.Paths) {
		return true
	}

	parts := strings.SplitN(reference, "/", 2)
	if len(parts) < 2 {
		return false
	}

	registry := parts[0]
	path := parts[1]

	for _, r := range s.Registries {
		if r == registry && match(path, s.Paths) {
			return true
		}
	}

	return false
}

func match(reference string, paths []*regexp.Regexp) bool {
	for _, path := range paths {
		if path.Match([]byte(reference)) {
			return true
		}
	}

	return false
}

func Match(reference string, strategies []*Strategy) *Strategy {
	for _, strategy := range strategies {
		if strategy.Match(reference) {
			return strategy
		}
	}

	return nil
}
