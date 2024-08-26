package registry

import (
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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

func sha1Sum(str string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(str)))
}

func Test_parseLocalReference(t *testing.T) {
	Endpoint = "kube-image-keeper-registry:5000"

	tests := []struct {
		name                    string
		image                   string
		expectedDestinationName string
		wantErr                 string
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
			wantErr: "could not parse reference: alpine:tag:another-tag",
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reference, err := parseLocalReference(tt.image)

			if tt.wantErr != "" {
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
		wantErr    string
		errType    error
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
			wantErr:    "response did not include Content-Type header",
			errType:    errors.New(""),
		},
		{
			name:       "Invalid reference",
			image:      "alpine:alpine:latest",
			httpStatus: http.StatusOK,
			wantErr:    "could not parse reference",
			errType:    &name.ErrBadName{},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)
			server := ghttp.NewServer()
			defer server.Close()

			headResponse := "..."
			if tt.wantErr != "" {
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
			if tt.wantErr != "" {
				err2 := errors.Unwrap(err)
				if err2 != nil {
					err = err2
				}
				g.Expect(err).To(BeAssignableToTypeOf(tt.errType))
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(isCached).To(Equal(tt.httpStatus == http.StatusOK && tt.wantErr == ""))
		})
	}
}

func Test_DeleteImage(t *testing.T) {
	tests := []struct {
		name              string
		image             string
		httpStatus        int
		headRandomlyFails bool
		wantErr           string
		errType           error
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
			wantErr:    "could not parse reference",
			errType:    &name.ErrBadName{},
		},
		{
			name:       "Image not found",
			image:      "image-not-found",
			httpStatus: http.StatusNotFound,
		},
		{
			name:       "Unauthorized",
			image:      "alpine",
			httpStatus: http.StatusUnauthorized,
			errType:    &transport.Error{},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)
			server := ghttp.NewServer()
			defer server.Close()

			server.AppendHandlers(
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, "/v2/docker.io/library/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, "...", mockedHeadImageHeader),
				),
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodDelete, "/v2/docker.io/library/"+tt.image+"/manifests/"+mockedDigest),
					gh.RespondWith(http.StatusOK, ""),
				),
			)

			Endpoint = server.Addr()
			err := DeleteImage(tt.image)
			if tt.errType != nil {
				g.Expect(err).To(BeAssignableToTypeOf(tt.errType))
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func Test_CacheImage(t *testing.T) {
	tests := []struct {
		name            string
		image           string
		httpStatus      int
		httpResponse    string
		putHttpStatus   int
		putHttpResponse string
		wantErr         string
		errType         error
	}{
		{
			name:  "Basic",
			image: "alpine",
		},
		{
			name:            "Could not write",
			image:           "alpine",
			putHttpStatus:   http.StatusUnauthorized,
			putHttpResponse: "unauthorized",
			wantErr:         "unauthorized",
			errType:         &transport.Error{},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := ghttp.NewGHTTPWithGomega(g)

			digestSha := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			layerSha := "sha256:5d20c808ce198565ff70b3ed23a991dd49afac45dece63474b27ce6ed036adc6"
			if tt.httpResponse == "" {
				tt.httpResponse = "{\"config\":{\"digest\":\"" + digestSha + "\",\"mediaType\":\"application/vnd.docker.container.image.v1+json\",\"size\":0},\"layers\":[{\"digest\":\"" + layerSha + "\",\"mediaType\":\"application/vnd.docker.image.rootfs.diff.tar.gzip\",\"size\":2107098}],\"mediaType\":\"application/vnd.docker.distribution.manifest.v2+json\",\"schemaVersion\":2}"
			}
			if tt.httpStatus == 0 {
				tt.httpStatus = http.StatusOK
			}
			if tt.putHttpStatus == 0 {
				tt.putHttpStatus = http.StatusOK
			}

			originRegistry := ghttp.NewServer()
			defer originRegistry.Close()
			originRegistry.AppendHandlers(
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodGet, "/v2/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, tt.httpResponse, mockedHeadImageHeader),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodGet, "/v2/"+tt.image+"/blobs/"+digestSha),
					gh.RespondWith(http.StatusOK, "", mockedHeadImageHeader),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodGet, "/v2/"+tt.image+"/blobs/"+digestSha),
					gh.RespondWith(http.StatusOK, "", mockedHeadImageHeader),
				),
			)

			originRegistryName := strings.ReplaceAll(originRegistry.Addr(), ":", "-")

			cacheRegistry := ghttp.NewServer()
			defer cacheRegistry.Close()
			// those paths can be called in any order
			pathMatcher := Or(Equal("/v2/"+originRegistryName+"/"+tt.image+"/blobs/"+layerSha), Equal("/v2/"+originRegistryName+"/"+tt.image+"/blobs/"+digestSha))
			cacheRegistry.AppendHandlers(
				mockV2Endpoint(gh),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, "/v2/"+originRegistryName+"/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.httpStatus, tt.httpResponse, mockedHeadImageHeader),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, pathMatcher),
					gh.RespondWith(http.StatusOK, "...", mockedHeadImageHeader),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodHead, pathMatcher),
					gh.RespondWith(http.StatusOK, "...", mockedHeadImageHeader),
				),
				ghttp.CombineHandlers(
					gh.VerifyRequest(http.MethodPut, "/v2/"+originRegistryName+"/"+tt.image+"/manifests/latest"),
					gh.RespondWith(tt.putHttpStatus, tt.putHttpResponse),
				),
			)

			Endpoint = cacheRegistry.Addr()
			imageName := originRegistry.Addr() + "/" + tt.image

			sourceRef, err := name.ParseReference(imageName)
			g.Expect(err).To(BeNil())

			desc, err := remote.Get(sourceRef)
			g.Expect(err).To(BeNil())

			err = CacheImage(imageName, desc, []string{"amd64"}, nil, logr.Discard())
			if tt.wantErr != "" {
				g.Expect(err).To(BeAssignableToTypeOf(tt.errType))
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
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

func TestContainerAnnotationKey(t *testing.T) {
	tests := []struct {
		name                  string
		containerName         string
		initContainer         bool
		expectedAnnotationKey string
	}{
		{
			name:                  "Basic",
			containerName:         "backend",
			expectedAnnotationKey: "original-image-backend",
		},
		{
			name:                  "Basic init",
			containerName:         "backend",
			initContainer:         true,
			expectedAnnotationKey: "original-init-image-backend",
		},
		{
			name:                  "Long name",
			containerName:         "my-incredible-and-marvelous-backend-that-rocks-so-much-it-has-become-a-mountain",
			expectedAnnotationKey: "original-image-" + sha1Sum("my-incredible-and-marvelous-backend-that-rocks-so-much-it-has-become-a-mountain"),
		},
		{
			name:                  "63 chars output",
			containerName:         "my-incredible-and-marvelous-backend-that-rocks-s",
			expectedAnnotationKey: "original-image-my-incredible-and-marvelous-backend-that-rocks-s",
		},
		{
			name:                  "63 chars output init",
			containerName:         "my-incredible-and-marvelous-backend-that-ro",
			initContainer:         true,
			expectedAnnotationKey: "original-init-image-my-incredible-and-marvelous-backend-that-ro",
		},
		{
			name:                  "64 chars output",
			containerName:         "my-incredible-and-marvelous-backend-that-rocks-so",
			expectedAnnotationKey: "original-image-" + sha1Sum("my-incredible-and-marvelous-backend-that-rocks-so"),
		},
		{
			name:                  "64 chars output init",
			containerName:         "my-incredible-and-marvelous-backend-that-roc",
			initContainer:         true,
			expectedAnnotationKey: "original-init-image-" + sha1Sum("my-incredible-and-marvelous-backend-that-roc"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			annotationKey := ContainerAnnotationKey(tt.containerName, tt.initContainer)
			g.Expect(annotationKey).To(Equal(tt.expectedAnnotationKey))
		})
	}
}
