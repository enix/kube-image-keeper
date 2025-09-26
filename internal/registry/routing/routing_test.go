package routing

import (
	"regexp"
	"testing"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	_ "github.com/enix/kube-image-keeper/internal/testsetup"
	. "github.com/onsi/gomega"
)

func TestMatch(t *testing.T) {
	routing := Routing{
		Strategies: []Strategy{
			{
				Paths:      []*regexp.Regexp{regexp.MustCompile("enix/x509-certificate-exporter"), regexp.MustCompile("nginx"), regexp.MustCompile("^bitnami/.+$")},
				Registries: []string{"docker.io", "ghcr.io"},
			},
			{
				Paths:      []*regexp.Regexp{regexp.MustCompile("^enix/.+")},
				Registries: []string{"quay.io", "harbor.enix.io"},
			},
			{
				Paths:      []*regexp.Regexp{regexp.MustCompile("enix/.+")},
				Registries: []string{"quay.io", "harbor.enix.io"},
			},
		},
	}

	tests := []struct {
		name          string
		reference     *kuikv1alpha1.ImageReference
		routing       *Routing
		expectedMatch *Strategy
	}{
		{
			name:      "Empty strategies list",
			reference: kuikv1alpha1.NewImageReference("", "nginx"),
			routing:   &Routing{},
		},
		{
			name:          "Match first",
			reference:     kuikv1alpha1.NewImageReference("", "enix/x509-certificate-exporter"),
			routing:       &routing,
			expectedMatch: &routing.Strategies[0],
		},
		{
			name:          "Match second",
			reference:     kuikv1alpha1.NewImageReference("", "enix/topomatik"),
			routing:       &routing,
			expectedMatch: &routing.Strategies[1],
		},
		{
			name:          "Match nothing",
			reference:     kuikv1alpha1.NewImageReference("", "backup-bitnami/alpine"),
			routing:       &routing,
			expectedMatch: nil,
		},
		{
			name:          "Match startsWith regex with registry",
			reference:     kuikv1alpha1.NewImageReference("ghcr.io", "bitnami/alpine"),
			routing:       &routing,
			expectedMatch: &routing.Strategies[0],
		},
		{
			name:          "Match startsWith regex with registry bis",
			reference:     kuikv1alpha1.NewImageReference("ghcr.io", "enix/topomatik"),
			routing:       &routing,
			expectedMatch: &routing.Strategies[2],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			strategy := tt.routing.Match(tt.reference)
			g.Expect(strategy).To(Equal(tt.expectedMatch))
		})
	}
}
