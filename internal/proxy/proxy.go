package proxy

import (
	"errors"
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

func proxyRegistry(c *gin.Context, endpoint string, image string, localCache bool) error {
	klog.V(2).InfoS("proxying registry", "endpoint", endpoint, "image", image)

	remote, err := url.Parse(endpoint)
	if err != nil {
		panic(err)
	}

	parts := strings.Split(image, "/")
	originRegistry := ""
	if localCache && len(parts) > 2 {
		originRegistry = parts[0] + "/"
		image = strings.Join(parts[1:], "/")
	} else {
		image = strings.Join(parts, "/")
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)

	proxy.Director = func(req *http.Request) {
		req.Header = c.Request.Header
		req.Host = remote.Host
		req.URL.Scheme = remote.Scheme
		req.URL.Host = remote.Host
		req.URL.Path = "/v2/" + originRegistry + strings.Join(strings.Split(req.URL.Path, "/")[2:], "/")

		// To prevent "X-Forwarded-For: 127.0.0.1, 127.0.0.1" which produce a HTTP 400 error
		req.Header.Del("X-Forwarded-For")

		bearer, err := NewBearer(endpoint, req.URL.Path)
		if err != nil {
			panic(err)
		}
		token := bearer.GetToken()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if localCache && resp.StatusCode != http.StatusOK {
			return errors.New(resp.Status)
		}
		return nil
	}

	var proxyError error
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		proxyError = err
	}

	proxy.ServeHTTP(c.Writer, c.Request)

	return proxyError
}
