package routing

import (
	"regexp"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

type ActiveCheck struct {
	Enabled bool          `koanf:"enabled"`
	Timeout time.Duration `koanf:"timeout"`
	// Timeoutstr string        `koanf:"timeout"`
}

type Routing struct {
	Strategies  []Strategy  `koanf:"strategies"`
	ActiveCheck ActiveCheck `koanf:"activeCheck"`
}

type Strategy struct {
	Paths      []*regexp.Regexp `koanf:"paths"`
	Registries []string         `koanf:"registries"`
}

func (r *Routing) Match(reference *kuikv1alpha1.ImageReference) *Strategy {
	for i := range r.Strategies {
		if r.Strategies[i].Match(reference) {
			return &r.Strategies[i]
		}
	}

	return nil
}

func (r *Routing) MatchingRegistries(reference *kuikv1alpha1.ImageReference) []string {
	if strategy := r.Match(reference); strategy != nil {
		return strategy.Registries
	}
	return []string{reference.Registry}
}

func (s *Strategy) Match(reference *kuikv1alpha1.ImageReference) bool {
	if match(reference.Reference(), s.Paths) {
		return true
	}

	for _, r := range s.Registries {
		if r == reference.Registry && match(reference.Path, s.Paths) {
			return true
		}
	}

	return false
}

func match(path string, paths []*regexp.Regexp) bool {
	for _, p := range paths {
		if p.Match([]byte(path)) {
			return true
		}
	}

	return false
}
