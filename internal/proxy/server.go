package proxy

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	"k8s.io/klog/v2"
)

type Proxy struct {
	engine *gin.Engine
}

const (
	headerOriginRegistryKey = "Origin-Registry"
)

func New() *Proxy {
	return &Proxy{engine: gin.Default()}
}

func (p *Proxy) Serve() chan struct{} {
	r := p.engine

	{
		v2 := r.Group("/v2")
		v2.Use(p.UrlRewrite())
		v2.Any("*test", func(c *gin.Context) {})
	}

	internal := r.Group("/internal")
	{
		internal.GET("/", p.v2Endpoint)

		name := internal.Group("/:library/:name")
		{
			name.GET("/manifests/:reference", p.routeProxy)
			name.HEAD("/manifests/:reference", p.routeProxy)
			name.GET("/blobs/:digest", p.routeProxy)
		}
	}

	finished := make(chan struct{})
	go func() {
		r.Run(":8082")
		finished <- struct{}{}
	}()

	return finished
}

func (p *Proxy) UrlRewrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		var originRegistry string

		parts := strings.Split(c.Request.URL.Path[3:], "/")[1:]
		if len(parts) > 4 {
			originRegistry = strings.Join(parts[:len(parts)-4], "/")
			c.Request.URL.Path = "/" + strings.Join(parts[len(parts)-4:], "/")
		} else {
			originRegistry = "index.docker.io"
		}

		c.Request.URL.Path = "/internal" + c.Request.URL.Path
		c.Request.Header.Set(headerOriginRegistryKey, originRegistry)

		p.engine.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

func (p *Proxy) v2Endpoint(c *gin.Context) {
	proxyRegistry(c, registry.Protocol+registry.Endpoint, "", false)
}

func (p *Proxy) routeProxy(c *gin.Context) {
	image := p.getImage(c)
	if err := proxyRegistry(c, registry.Protocol+registry.Endpoint, image, true); err != nil {
		headerOriginRegistry := c.Request.Header.Get(headerOriginRegistryKey)
		klog.InfoS("cached image not available yet, proxying origin", "headerOriginRegistry", headerOriginRegistry)
		proxyRegistry(c, "https://"+headerOriginRegistry, image, false)
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
