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
	request, err := http.NewRequest("GET", "/v2", nil)
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
