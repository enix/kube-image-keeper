package proxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	"k8s.io/klog/v2"
)

var wwwAuthenticateRegexp = regexp.MustCompile(`(?P<key>\w+)="(?P<value>[^"]+)",?`)

func parseWwwAuthenticate(wwwAuthenticate string) map[string]string {
	challenge := strings.SplitN(wwwAuthenticate, " ", 2)[1]
	parts := wwwAuthenticateRegexp.FindAllStringSubmatch(challenge, -1)

	opts := map[string]string{}
	for _, part := range parts {
		opts[part[1]] = part[2]
	}

	return opts
}

func proxyOriginRegistry(c *gin.Context, endpoint string, image string) error {
	return proxyRegistry(c, endpoint, image, "")
}

func proxyRegistry(c *gin.Context, endpoint string, image string, originRegistry string) error {
	klog.V(2).InfoS("proxying registry", "endpoint", endpoint, "image", image)

	remote, err := url.Parse(endpoint)
	if err != nil {
		return err
	}

	if originRegistry != "" {
		originRegistry += "/"
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)

	var proxyError error

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
			proxyError = err
			return
		}
		token := bearer.GetToken()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if endpoint == registry.Protocol+registry.Endpoint && resp.StatusCode != http.StatusOK {
			return errors.New(resp.Status)
		}
		return nil
	}

	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		proxyError = err
	}

	proxy.ServeHTTP(c.Writer, c.Request)

	return proxyError
}
