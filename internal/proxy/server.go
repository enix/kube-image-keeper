package proxy

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"gitlab.enix.io/products/docker-cache-registry/internal/cache"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	"k8s.io/klog/v2"
)

type Proxy struct {
	cacheController *cache.Cache
}

func New() (*Proxy, error) {
	c, err := cache.New()
	return &Proxy{cacheController: c}, err
}

func (p *Proxy) Serve() chan struct{} {
	r := gin.Default()

	v2 := r.Group("/v2")
	{
		v2.GET("/", p.v2Endpoint)

		name := v2.Group("/:library/:name")
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

func (p *Proxy) v2Endpoint(c *gin.Context) {
	image := p.getImage(c)
	proxyRegistry(c, registry.Protocol+registry.Endpoint, image)
}

func (p *Proxy) routeProxy(c *gin.Context) {
	image := p.getImage(c)
	cachedImage, err := p.cacheController.GetCachedImage(image)
	if err != nil {
		c.JSON(401, &gin.H{
			"errors": []gin.H{
				{
					"code":    "NAME_UNKNOWN",
					"message": err.Error(),
				},
			},
		})

		return
	}

	if cachedImage.Status.PulledAt != 0 {
		klog.Info("cached image available, proxying cache registry")
		proxyRegistry(c, registry.Protocol+registry.Endpoint, cachedImage.Spec.SourceImage)
	} else {
		klog.Info("cached image not available yet, proxying origin")
		proxyRegistry(c, "https://index.docker.io", cachedImage.Spec.SourceImage)
	}
}

func (p *Proxy) getImage(c *gin.Context) string {
	image := fmt.Sprintf("%s/%s", c.Param("library"), c.Param("name"))
	return image
}
