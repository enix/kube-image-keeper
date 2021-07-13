package proxy

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"gitlab.enix.io/products/docker-cache-registry/internal/cache"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
)

type proxy struct {
	cacheController *cache.Cache
}

func Serve(cacheController *cache.Cache) chan struct{} {
	r := gin.Default()
	p := proxy{cacheController: cacheController}

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
		r.Run()
		finished <- struct{}{}
	}()

	return finished
}

func (p *proxy) v2Endpoint(c *gin.Context) {
	image := p.getImage(c, false)
	proxyRegistry(c, registry.Protocol+registry.Endpoint, image)
}

func (p *proxy) routeProxy(c *gin.Context) {
	image := p.getImage(c, true)
	imageInfo, err := p.cacheController.GetImageInfo(image)
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

	if imageInfo.Cached {
		proxyRegistry(c, registry.Protocol+registry.Endpoint, imageInfo.SourceImage)
	} else {
		proxyRegistry(c, "https://index.docker.io", imageInfo.SourceImage)
	}
}

func (p *proxy) getImage(c *gin.Context, withHost bool) string {
	image := fmt.Sprintf("%s/%s", c.Param("library"), c.Param("name"))
	if !withHost {
		return image
	}
	return fmt.Sprintf("%s/%s", c.Request.Host, image)
}
