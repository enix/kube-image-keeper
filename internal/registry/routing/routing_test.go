package routing

import (
	"regexp"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func (s *Strategy) GomegaString() string {
	paths := []string{}
	for _, path := range s.Paths {
		paths = append(paths, path.String())
	}
	return strings.Join(paths, " | ")
}

func TestMatch(t *testing.T) {
	strategies := []*Strategy{
		{
			Paths:      []*regexp.Regexp{regexp.MustCompile("enix/x509-exporter"), regexp.MustCompile("nginx"), regexp.MustCompile("^bitnami/.+$")},
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
	}

	tests := []struct {
		name          string
		reference     string
		strategies    []*Strategy
		expectedMatch *Strategy
	}{
		{
			name:      "Empty strategies list",
			reference: "nginx",
		},
		{
			name:          "Match first",
			reference:     "enix/x509-exporter",
			strategies:    strategies,
			expectedMatch: strategies[0],
		},
		{
			name:          "Match second",
			reference:     "enix/topomatik",
			strategies:    strategies,
			expectedMatch: strategies[1],
		},
		{
			name:          "Match nothing",
			reference:     "backup-bitnami/alpine",
			strategies:    strategies,
			expectedMatch: nil,
		},
		{
			name:          "Match startsWith regex with registry",
			reference:     "ghcr.io/bitnami/alpine",
			strategies:    strategies,
			expectedMatch: strategies[0],
		},
		{
			name:          "Match startsWith regex with registry bis",
			reference:     "ghcr.io/enix/topomatik",
			strategies:    strategies,
			expectedMatch: strategies[2],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			strategy := Match(tt.reference, tt.strategies)
			g.Expect(strategy).To(Equal(tt.expectedMatch))
		})
	}
}
