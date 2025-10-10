package routing

import (
	"regexp"
	"testing"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	_ "github.com/enix/kube-image-keeper/internal/testsetup"
	. "github.com/onsi/gomega"
)

func TestMatch(t *testing.T) {
	conf := &config.Config{
		Strategies: []config.Strategy{
			{
				Paths: []*regexp.Regexp{regexp.MustCompile("enix/x509-certificate-exporter"), regexp.MustCompile("nginx"), regexp.MustCompile("^bitnami/.+$")},
				Registries: []config.Registry{
					{Url: "docker.io"},
					{Url: "ghcr.io"},
				},
			},
			{
				Paths: []*regexp.Regexp{regexp.MustCompile("^enix/.+")},
				Registries: []config.Registry{
					{Url: "quay.io"},
					{Url: "harbor.enix.io"},
				},
			},
			{
				Paths: []*regexp.Regexp{regexp.MustCompile("enix/.+")},
				Registries: []config.Registry{
					{Url: "quay.io"},
					{Url: "harbor.enix.io"},
				},
			},
		},
	}

	tests := []struct {
		name          string
		reference     *kuikv1alpha1.ImageReference
		config        *config.Config
		expectedMatch *config.Strategy
	}{
		{
			name:      "Empty strategies list",
			reference: kuikv1alpha1.NewImageReference("", "nginx"),
			config:    &config.Config{},
		},
		{
			name:          "Match first",
			reference:     kuikv1alpha1.NewImageReference("", "enix/x509-certificate-exporter"),
			config:        conf,
			expectedMatch: &conf.Strategies[0],
		},
		{
			name:          "Match second",
			reference:     kuikv1alpha1.NewImageReference("", "enix/topomatik"),
			config:        conf,
			expectedMatch: &conf.Strategies[1],
		},
		{
			name:          "Match nothing",
			reference:     kuikv1alpha1.NewImageReference("", "backup-bitnami/alpine"),
			config:        conf,
			expectedMatch: nil,
		},
		{
			name:          "Match startsWith regex with registry",
			reference:     kuikv1alpha1.NewImageReference("ghcr.io", "bitnami/alpine"),
			config:        conf,
			expectedMatch: &conf.Strategies[0],
		},
		{
			name:          "Match startsWith regex with registry bis",
			reference:     kuikv1alpha1.NewImageReference("ghcr.io", "enix/topomatik"),
			config:        conf,
			expectedMatch: &conf.Strategies[2],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			strategy := MatchingStrategy(tt.config, tt.reference)
			g.Expect(strategy).To(Equal(tt.expectedMatch))
		})
	}
}
