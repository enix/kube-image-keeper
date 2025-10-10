package routing

import (
	"regexp"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
)

func MatchingStrategy(conf *config.Config, reference *kuikv1alpha1.ImageReference) *config.Strategy {
	for i := range conf.Strategies {
		if Match(&conf.Strategies[i], reference) {
			return &conf.Strategies[i]
		}
	}

	return nil
}

func MatchingRegistries(conf *config.Config, reference *kuikv1alpha1.ImageReference) []config.Registry {
	if strategy := MatchingStrategy(conf, reference); strategy != nil {
		return strategy.Registries
	}
	return []config.Registry{
		{Url: reference.Registry},
	}
}

func Match(strategy *config.Strategy, reference *kuikv1alpha1.ImageReference) bool {
	if match(reference.Reference(), strategy.Paths) {
		return true
	}

	for _, r := range strategy.Registries {
		if r.Url == reference.Registry && match(reference.Path, strategy.Paths) {
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
