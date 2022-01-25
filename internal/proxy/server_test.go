package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
)

type ResponseWriterPatched struct {
	gin.ResponseWriter
}

func (w *ResponseWriterPatched) CloseNotify() <-chan bool {
	return nil
}

func init() {
	gin.SetMode(gin.TestMode)
}

func TestNew(t *testing.T) {
	g := NewWithT(t)
	proxy := New()
	g.Expect(proxy).To(Not(BeNil()))
	g.Expect(proxy.engine).To(Not(BeNil()))
}

func TestRewriteToInternalUrlMiddleware(t *testing.T) {
	g := NewWithT(t)

	// mock origin server
	gh := ghttp.NewGHTTPWithGomega(g)
	server := ghttp.NewServer()
	defer server.Close()
	server.AppendHandlers(
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/v2/"+server.Addr()+"/library/nginx/manifests/xxxxxx"),
			gh.RespondWith(401, nil, http.Header{
				"Www-Authenticate": []string{
					"Bearer realm=\"" + server.URL() + "/token\",service=\"registry.docker.io\",scope=\"repository:samalba/my-app:pull,push\"",
				},
			}),
		),
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/token"),
			gh.RespondWithJSONEncoded(http.StatusOK, &Bearer{
				Token:     "token",
				ExpiresIn: "3600",
			}),
		),
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/v2/"+server.Addr()+"/library/nginx/manifests/xxxxxx"),
			gh.RespondWith(200, "image manifest"),
		),
	)

	// mock proxy server
	recorder := httptest.NewRecorder()
	ctx, r := gin.CreateTestContext(recorder)
	proxy := NewWithEngine(r).Listen()

	// mock request
	registry.Endpoint = server.Addr()
	request, err := http.NewRequest("GET", "/v2/"+server.Addr()+"/library/nginx/manifests/xxxxxx", nil)
	g.Expect(err).To(BeNil())
	ctx.Request = request
	ctx.Writer = &ResponseWriterPatched{ctx.Writer}

	// execute middleware
	handler := proxy.RewriteToInternalUrlMiddleware()
	handler(ctx)

	// check specs
	g.Expect(ctx.Request.URL.Path).To(Equal("/internal/library/nginx/manifests/xxxxxx"))
	g.Expect(ctx.Request.Header.Get(headerOriginRegistryKey)).To(Equal(server.Addr()))
	g.Expect(recorder.Result().StatusCode).To(Equal(http.StatusOK))
	g.Expect(recorder.Body.String()).To(Equal("image manifest"))
}

func Test_v2Endpoint(t *testing.T) {
	g := NewWithT(t)

	// mock server
	gh := ghttp.NewGHTTPWithGomega(g)
	server := ghttp.NewServer()
	defer server.Close()
	server.AppendHandlers(
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/v2/"),
			gh.RespondWith(401, nil, http.Header{
				"Www-Authenticate": []string{
					"Bearer realm=\"" + server.URL() + "/token\",service=\"registry.docker.io\",scope=\"repository:samalba/my-app:pull,push\"",
				},
			}),
		),
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/token"),
			gh.RespondWithJSONEncoded(http.StatusOK, &Bearer{
				Token:     "token",
				ExpiresIn: "3600",
			}),
		),
		ghttp.CombineHandlers(
			gh.VerifyRequest(http.MethodGet, "/v2/"),
			gh.RespondWith(200, "v2 endpoint"),
		),
	)

	// mock proxy server
	recorder := httptest.NewRecorder()
	ctx, r := gin.CreateTestContext(recorder)
	proxy := NewWithEngine(r).Listen()

	// mock request
	registry.Endpoint = server.Addr()
	request, err := http.NewRequest("GET", "/internal", nil)
	g.Expect(err).To(BeNil())
	ctx.Request = request
	ctx.Writer = &ResponseWriterPatched{ctx.Writer}

	proxy.v2Endpoint(ctx)
	g.Expect(recorder.Body.String()).To(Equal("v2 endpoint"))
}

func TestGetImage(t *testing.T) {
	tests := []struct {
		name      string
		library   string
		imageName string
		reference string
		digest    string
		expected  string
	}{
		{
			name:      "Reference only",
			library:   "library",
			imageName: "image",
			reference: "reference",
			expected:  "library/image:reference",
		},
		{
			name:      "Digest only",
			library:   "library",
			imageName: "image",
			digest:    "digest",
			expected:  "library/image@digest",
		},
		{
			name:      "Reference + digest",
			library:   "library",
			imageName: "image",
			reference: "reference",
			digest:    "digest",
			expected:  "library/image:reference",
		},
	}

	proxy := New()
	g := NewWithT(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &gin.Context{
				Params: gin.Params{
					gin.Param{
						Key:   "library",
						Value: tt.library,
					},
					gin.Param{
						Key:   "name",
						Value: tt.imageName,
					},
					gin.Param{
						Key:   "reference",
						Value: tt.reference,
					},
					gin.Param{
						Key:   "digest",
						Value: tt.digest,
					},
				},
			}

			image := proxy.getImage(ctx)
			g.Expect(image).To(Equal(tt.expected))
		})
	}
}

func TestRewriteToInternalUrl(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantPath   string
		wantOrigin string
	}{
		{
			name:       "Empty path",
			path:       "",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "Empty path with trailing slash",
			path:       "/",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint",
			path:       "/v2",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint with trailing slash",
			path:       "/v2/",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint with trailing slash",
			path:       "/v2/docker.io/library",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "From standard library",
			path:       "/v2/docker.io/library/nginx/manifests/xxxxx",
			wantPath:   "/internal/library/nginx/manifests/xxxxx",
			wantOrigin: "docker.io",
		},
		{
			name:       "From user library",
			path:       "/v2/docker.io/enix/san-iscsi-csi/manifests/xxxxx",
			wantPath:   "/internal/enix/san-iscsi-csi/manifests/xxxxx",
			wantOrigin: "docker.io",
		},
		{
			name:       "From custom registry with tag and digest",
			path:       "/v2/quay.io/prometheus/busybox/manifests/glibc@sha256:9c2d6d09bbc625f07587d321f4b2aec88e44ae752804ba905b048c8bba1b3025",
			wantPath:   "/internal/prometheus/busybox/manifests/glibc@sha256:9c2d6d09bbc625f07587d321f4b2aec88e44ae752804ba905b048c8bba1b3025",
			wantOrigin: "quay.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, origin := RewriteToInternalUrl(tt.path)
			if path != tt.wantPath {
				t.Errorf("RewriteToInternalUrl() = (%v, _) want (%v, _)", path, tt.wantPath)
			}
			if origin != tt.wantOrigin {
				t.Errorf("RewriteToInternalUrl() = (_, %v) want (_, %v)", origin, tt.wantOrigin)
			}
		})
	}
}
