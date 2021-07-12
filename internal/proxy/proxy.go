package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

func parseWwwAuthenticate(wwwAuthenticate string) map[string]string {
	parts := strings.SplitN(wwwAuthenticate, " ", 2)
	parts = strings.Split(parts[1], ",")

	opts := map[string]string{}
	for _, part := range parts {
		vals := strings.SplitN(part, "=", 2)
		key := vals[0]
		val := strings.Trim(vals[1], "\",")
		opts[key] = val
	}

	return opts
}

func proxyRegistry(c *gin.Context, endpoint string, image string) {
	klog.V(2).InfoS("proxying registry", "endpoint", endpoint)

	remote, err := url.Parse(endpoint)
	if err != nil {
		panic(err)
	}

	scope := fmt.Sprintf("repository:%s:pull", image)
	bearer, err := NewBearer(endpoint, scope)
	if err != nil {
		panic(err)
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)

	proxy.Director = func(req *http.Request) {
		req.Header = c.Request.Header
		req.Host = remote.Host
		req.URL.Scheme = remote.Scheme
		req.URL.Host = remote.Host

		token := bearer.GetToken()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}
