package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/gin-gonic/gin"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
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
	proxy := New(dummyK8sClient, ":8080", []string{})
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
			gh.VerifyRequest(http.MethodGet, "/v2"),
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
			gh.VerifyRequest(http.MethodGet, "/v2"),
			gh.RespondWith(200, "v2 endpoint"),
		),
	)

	// mock proxy server
	recorder := httptest.NewRecorder()
	ctx, r := gin.CreateTestContext(recorder)
	proxy := NewWithEngine(dummyK8sClient, r).Serve()

	// mock request
	registry.Endpoint = server.Addr()
	request, err := http.NewRequest("GET", "/v2", nil)
	g.Expect(err).To(BeNil())
	ctx.Request = request
	ctx.Writer = &ResponseWriterPatched{ctx.Writer}

	proxy.v2Endpoint(ctx)
	g.Expect(recorder.Body.String()).To(Equal("v2 endpoint"))
}

func Test_handleOriginRegistryPort(t *testing.T) {
	tests := []struct {
		name           string
		originRegistry string
		expectedOutput string
	}{
		{
			name:           "Ip address",
			originRegistry: "127.0.0.1",
			expectedOutput: "127.0.0.1",
		},
		{
			name:           "Ip address + port",
			originRegistry: "127.0.0.1-5000",
			expectedOutput: "127.0.0.1:5000",
		},
		{
			name:           "Domain name",
			originRegistry: "enix.io",
			expectedOutput: "enix.io",
		},
		{
			name:           "Domain name + port",
			originRegistry: "enix.io-5000",
			expectedOutput: "enix.io:5000",
		},
		{
			name:           "Domain name with number + port",
			originRegistry: "regsitry-2.enix.io-5000",
			expectedOutput: "regsitry-2.enix.io:5000",
		},

		{
			name:           "Domain name with number + port with same value",
			originRegistry: "regsitry-5000.enix.io-5000",
			expectedOutput: "regsitry-5000.enix.io:5000",
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originRegistry := handleOriginRegistryPort(tt.originRegistry)
			g.Expect(originRegistry).To(Equal(tt.expectedOutput))
		})
	}
}
