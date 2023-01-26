package registry

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/gomega"
)

func sha224(str string) string {
	return fmt.Sprintf("%x", sha256.Sum224([]byte(str)))
}

func Test_getDestinationName(t *testing.T) {
	tests := []struct {
		name                    string
		image                   string
		expectedDestinationName string
		wantErr                 error
	}{
		{
			name:                    "Basic",
			image:                   "alpine",
			expectedDestinationName: "kube-image-keeper-service:5000/docker.io/library/alpine:latest",
		},
		{
			name:                    "docker.io",
			image:                   "docker.io/library/alpine",
			expectedDestinationName: "kube-image-keeper-service:5000/docker.io/library/alpine:latest",
		},
		{
			name:                    "index.docker.io",
			image:                   "index.docker.io/library/alpine:3.14",
			expectedDestinationName: "kube-image-keeper-service:5000/docker.io/library/alpine:3.14",
		},
		{
			name:                    "Non default registry with port",
			image:                   "some-gitlab-registry.com:5000/group/another-group/project/backend",
			expectedDestinationName: "kube-image-keeper-service:5000/some-gitlab-registry.com-5000/group/another-group/project/backend:latest",
		},
		{
			name:    "Invalid source name",
			image:   "alpine:tag:another-tag",
			wantErr: name.NewErrBadName("could not parse reference: alpine:tag:another-tag"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, err := getDestinationName(tt.image)

			if tt.wantErr != nil {
				g.Expect(err).To(MatchError(tt.wantErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(label).To(Equal(tt.expectedDestinationName))

		})
	}
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
			name:                   "Many parts",
			image:                  "some-gitlab-registry.com:5000/group/another-group/project/backend:v1.0.0",
			expectedSanitizedImage: "some-gitlab-registry.com-5000-group-another-group-project-backend-v1.0.0",
		},
		{
			name:                   "Special chars",
			image:                  "abc123!@#$%*()_+[]{}\\|\".,></?-=",
			expectedSanitizedImage: "abc123",
		},
		{
			name:                   "Special chars 2",
			image:                  "abc123++@++yx.z",
			expectedSanitizedImage: "abc123-yx.z",
		},
		{
			name:                   "Special chars 3",
			image:                  "abc123++.++yxz",
			expectedSanitizedImage: "abc123-yxz",
		},
		{
			name:                   "Capital letters",
			image:                  "abcEFG-foo#bar",
			expectedSanitizedImage: "abcefg-foo-bar",
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
