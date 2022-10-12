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
	proxyRegistry(c, registry.Protocol+registry.Endpoint, true)
}

func (p *Proxy) routeProxy(c *gin.Context) {
	image := p.getImage(c)
	originRegistry := c.Param("originRegistry")

	klog.InfoS("proxying request", "image", image, "originRegistry", originRegistry)
	if err := proxyRegistry(c, registry.Protocol+registry.Endpoint, false); err != nil {
		if strings.HasSuffix(originRegistry, "docker.io") {
			originRegistry = "index.docker.io"
		}
		klog.InfoS("cached image is not available, proxying origin", "originRegistry", originRegistry, "error", err)
		proxyRegistry(c, "https://"+originRegistry, true)
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
