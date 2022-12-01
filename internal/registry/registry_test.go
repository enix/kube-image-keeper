package registry

import (
	"crypto/sha256"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

func sha224(str string) string {
	return fmt.Sprintf("%x", sha256.Sum224([]byte(str)))
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name                   string
		image                  string
		expectedSanitizedImage string
	}{
		{
			name:                   "Basic",
			image:                  "docker.io/library/alpine",
			expectedSanitizedImage: "docker.io-library-alpine",
		},
		{
			name:                   "Advanced",
			image:                  "some-gitlab-registry.com:5000/group/another-group/project/backend:v1.0.0",
			expectedSanitizedImage: "some-gitlab-registry.com-5000-group-another-group-project-backend-v1.0.0",
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label := SanitizeName(tt.image)
			g.Expect(label).To(Equal(tt.expectedSanitizedImage))
		})
	}
}

func TestRepositoryLabel(t *testing.T) {
	tests := []struct {
		name           string
		repositoryName string
		expectedLabel  string
	}{
		{
			name:           "Basic",
			repositoryName: "docker.io/library/alpine",
			expectedLabel:  "docker.io-library-alpine",
		},
		{
			name:           "Long name",
			repositoryName: "docker.io/rancher/mirrored-prometheus-operator-prometheus-operator",
			expectedLabel:  sha224("docker.io-rancher-mirrored-prometheus-operator-prometheus-operator"),
		},
		{
			name:           "63 chars",
			repositoryName: "docker.io/rancher/mirrored-prometheus-operator-prometheus-opera",
			expectedLabel:  "docker.io-rancher-mirrored-prometheus-operator-prometheus-opera",
		},
		{
			name:           "64 chars",
			repositoryName: "docker.io/rancher/mirrored-prometheus-operator-prometheus-operat",
			expectedLabel:  sha224("docker.io-rancher-mirrored-prometheus-operator-prometheus-operat"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label := RepositoryLabel(tt.repositoryName)
			g.Expect(label).To(Equal(tt.expectedLabel))
		})
	}
}
