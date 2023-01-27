package registry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
)

var mockedDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
var mockedHeadImageHeader = http.Header{
	"Docker-Content-Digest": []string{mockedDigest},
}

func mockV2Endpoint(gh *ghttp.GHTTPWithGomega) http.HandlerFunc {
	return ghttp.CombineHandlers(
		gh.VerifyRequest(http.MethodGet, "/v2/"),
		gh.RespondWith(http.StatusOK, ""),
	)
}

func sha224(str string) string {
	return fmt.Sprintf("%x", sha256.Sum224([]byte(str)))
}

func Test_parseLocalReference(t *testing.T) {
	tests := []struct {
		name                    string
		image                   string
		expectedDestinationName string
		wantErr                 error
	}{
		{
			name:                    "Basic",
			image:                   "alpine",
			expectedDestinationName: Endpoint + "/docker.io/library/alpine:latest",
		},
		{
			name:                    "docker.io",
			image:                   "docker.io/library/alpine",
			expectedDestinationName: Endpoint + "/docker.io/library/alpine:latest",
		},
		{
			name:                    "index.docker.io",
			image:                   "index.docker.io/library/alpine:3.14",
			expectedDestinationName: Endpoint + "/docker.io/library/alpine:3.14",
		},
		{
			name:                    "Non default registry with port",
			image:                   "some-gitlab-registry.com:5000/group/another-group/project/backend",
			expectedDestinationName: Endpoint + "/some-gitlab-registry.com-5000/group/another-group/project/backend:latest",
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
			reference, err := parseLocalReference(tt.image)

			if tt.wantErr != nil {
				g.Expect(err).To(MatchError(tt.wantErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(reference).ToNot(BeNil())
				g.Expect(reference.Name()).To(Equal(tt.expectedDestinationName))
			}
		})
	}
}

func Test_ImageIsCached(t *testing.T) {
	tests := []struct {
		name       string
		image      string
		httpStatus int
		wantErr    error
	}{
		{
			name:       "Exists",
			image:      "alpine",
			httpStatus: http.StatusOK,
		},
		{
			name:       "Don't exists",
			image:      "alpine",
			httpStatus: http.StatusNotFound,
		},
		{
			name:       "Missing header",
			image:      "alpine",
			httpStatus: http.StatusOK,
			wantErr:    errors.New("response did not include Content-Type header"),
		},
		{
			name:       "Invalid reference",
			image:      "alpine:alpine:latest",
			httpStatus: http.StatusOK,
			wantErr:    name.NewErrBadName("could not parse reference"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)
			server := ghttp.NewServer()
			defer server.Close()

			headResponse := "..."
			if tt.wantErr != nil {
				headResponse = "" // trigger missing Content-Type header error
			}

			server.AppendHandlers(
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, "/v2/docker.io/library/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, headResponse, mockedHeadImageHeader),
				),
			)

			Endpoint = server.Addr()
			isCached, err := ImageIsCached(tt.image)
			if tt.wantErr != nil {
				g.Expect(err).To(BeAssignableToTypeOf(tt.wantErr))
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr.Error())))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(isCached).To(Equal(tt.httpStatus == http.StatusOK && tt.wantErr == nil))
		})
	}
}

func Test_DeleteImage(t *testing.T) {
	tests := []struct {
		name              string
		image             string
		httpStatus        int
		headRandomlyFails bool
		wantErr           error
	}{
		{
			name:       "Exists",
			image:      "alpine",
			httpStatus: http.StatusOK,
		},
		{
			name:       "Invalid reference",
			image:      "alpine:alpine:latest",
			httpStatus: http.StatusOK,
			wantErr:    name.NewErrBadName("could not parse reference"),
		},
		{
			name:       "Don't exists",
			image:      "alpine",
			httpStatus: http.StatusNotFound,
		},
		{
			name:              "Second head randomly fails",
			image:             "alpine",
			headRandomlyFails: true,
			httpStatus:        http.StatusOK,
			wantErr:           errors.New("response did not include Content-Type header"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)
			server := ghttp.NewServer()
			defer server.Close()

			secondHeadResponse := "..."
			if tt.headRandomlyFails {
				secondHeadResponse = "" // trigger missing Content-Type header error
			}

			server.AppendHandlers(
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, "/v2/docker.io/library/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, "...", mockedHeadImageHeader),
				),
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, "/v2/docker.io/library/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, secondHeadResponse, mockedHeadImageHeader),
				),
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodDelete, "/v2/docker.io/library/"+tt.image+"/manifests/"+mockedDigest),
					gh.RespondWith(http.StatusOK, ""),
				),
			)

			Endpoint = server.Addr()
			err := DeleteImage(tt.image)
			if tt.wantErr != nil {
				g.Expect(err).To(BeAssignableToTypeOf(tt.wantErr))
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr.Error())))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
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
