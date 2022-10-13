package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	"k8s.io/klog/v2"
)

type Proxy struct {
	engine *gin.Engine
}

func New() *Proxy {
	return &Proxy{engine: gin.Default()}
}

func NewWithEngine(engine *gin.Engine) *Proxy {
	return &Proxy{engine: engine}
}

func (p *Proxy) Listen() *Proxy {
	r := p.engine

	v2 := r.Group("/v2")
	{
		v2.GET("/", p.v2Endpoint)

		name := v2.Group("/:originRegistry/:library/:name")
		{
			name.GET("/manifests/:reference", p.routeProxy)
			name.HEAD("/manifests/:reference", p.routeProxy)
			name.GET("/blobs/:digest", p.routeProxy)
		}
	}

	return p
}

func (p *Proxy) Serve() chan struct{} {
	finished := make(chan struct{})
	go func() {
		p.engine.Run(":8082")
		finished <- struct{}{}
	}()

	return finished
}

func (p *Proxy) v2Endpoint(c *gin.Context) {
	p.proxyRegistry(c, registry.Protocol+registry.Endpoint, true)
}

func (p *Proxy) routeProxy(c *gin.Context) {
	image := p.getImage(c)
	originRegistry := c.Param("originRegistry")

	klog.InfoS("proxying request", "image", image, "originRegistry", originRegistry)
	if err := p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false); err != nil {
		if strings.HasSuffix(originRegistry, "docker.io") {
			originRegistry = "index.docker.io"
		}
		klog.InfoS("cached image is not available, proxying origin", "originRegistry", originRegistry, "error", err)
		p.proxyRegistry(c, "https://"+originRegistry, true)
	}
}

func (p *Proxy) getImage(c *gin.Context) string {
	library := c.Param("library")
	name := c.Param("name")
	reference := ":" + c.Param("reference")
	if reference == ":" {
		reference = "@" + c.Param("digest")
	}
	return fmt.Sprintf("%s/%s%s", library, name, reference)
}

func (p *Proxy) proxyRegistry(c *gin.Context, endpoint string, endpointIsOrigin bool) error {
	klog.V(2).InfoS("proxying registry", "endpoint", endpoint)

	remote, err := url.Parse(endpoint)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)

	var proxyError error

	proxy.Director = func(req *http.Request) {
		req.Header = c.Request.Header
		req.Host = remote.Host
		req.URL.Scheme = remote.Scheme
		req.URL.Host = remote.Host

		pathParts := strings.Split(req.URL.Path, "/")

		// In the cache registry, images are prefixed with their origin registry.
		// Thus, when proxying the cache, we need to keep the origin part, but we have to discard it when proxying the origin
		takePathFromIndex := 2
		if endpointIsOrigin && len(pathParts) > 2 {
			takePathFromIndex = 3
		}

		req.URL.Path = "/v2/" + strings.Join(pathParts[takePathFromIndex:], "/")

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
