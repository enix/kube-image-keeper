package routing

import (
	"regexp"
	"strings"
)

type Config struct {
	Strategies []Strategy `koanf:"strategies"`
}

type Strategy struct {
	Paths      []*regexp.Regexp `koanf:"paths"`
	Registries []string         `koanf:"registries"`
}

func (s *Strategy) Match(reference string) bool {
	if match(reference, s.Paths) {
		return true
	}

	parts := strings.SplitN(reference, "/", 2)
	if len(parts) < 2 {
		return false
	}

	registry, path := parts[0], parts[1]
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

func Match(reference string, strategies []Strategy) *Strategy {
	for i := range strategies {
		if strategies[i].Match(reference) {
			return &strategies[i]
		}
	}

	return nil
}
