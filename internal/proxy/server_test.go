package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var dummyK8sClient client.Client

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
	proxy := New(dummyK8sClient)
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
	proxy := NewWithEngine(dummyK8sClient, r).Listen()

	// mock request
	registry.Endpoint = server.Addr()
	request, err := http.NewRequest("GET", "/v2", nil)
	g.Expect(err).To(BeNil())
	ctx.Request = request
	ctx.Writer = &ResponseWriterPatched{ctx.Writer}

	proxy.v2Endpoint(ctx)
	g.Expect(recorder.Body.String()).To(Equal("v2 endpoint"))
}

func TestGetRepository(t *testing.T) {
	tests := []struct {
		name      string
		library   string
		imageName string
		expected  string
	}{
		{
			name:      "Basic 1",
			library:   "library",
			imageName: "image",
			expected:  "library/image",
		},
		{
			name:      "Basic 2",
			library:   "enix",
			imageName: "cache-registry",
			expected:  "enix/cache-registry",
		},
		{
			name:      "Empty image",
			library:   "library",
			imageName: "",
			expected:  "library/",
		},
		{
			name:      "Empty library",
			library:   "",
			imageName: "image",
			expected:  "/image",
		},
	}

	proxy := New(dummyK8sClient)
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
				},
			}

			image := proxy.getRepository(ctx)
			g.Expect(image).To(Equal(tt.expected))
		})
	}
}
