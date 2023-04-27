package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/docker/distribution/reference"
	kuikenixiov1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/gin-gonic/gin"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Proxy struct {
	engine    *gin.Engine
	k8sClient client.Client
}

func New(k8sClient client.Client) *Proxy {
	return &Proxy{
		k8sClient: k8sClient,
		engine:    gin.Default(),
	}
}

func NewWithEngine(k8sClient client.Client, engine *gin.Engine) *Proxy {
	return &Proxy{
		k8sClient: k8sClient,
		engine:    engine,
	}
}

func (p *Proxy) Serve() *Proxy {
	r := p.engine

	r.Use(recoveryMiddleware())

	v2 := r.Group("/v2")
	{
		pathRegex := regexp.MustCompile("/(.+)/((manifests|blobs)/.+)")

		v2.Any("*catch-all", func(c *gin.Context) {
			subPath := c.Request.URL.Path[len("/v2"):]
			if c.Request.Method == http.MethodGet && subPath == "/" {
				p.v2Endpoint(c)
				return
			}

			subMatches := pathRegex.FindStringSubmatch(subPath)
			if subMatches == nil {
				c.Status(404)
				return
			}

			ref, err := reference.ParseAnyReference(subMatches[1])
			if err != nil {
				_ = c.Error(err)
				return
			}
			image := ref.String()

			c.Request.URL.Path = fmt.Sprintf("/v2/%s/%s", image, subMatches[2])

			imageParts := strings.Split(image, "/")
			c.Params = append(c.Params, gin.Param{
				Key:   "originRegistry",
				Value: handleOriginRegistryPort(imageParts[0]),
			})
			c.Params = append(c.Params, gin.Param{
				Key:   "repository",
				Value: strings.Join(imageParts[1:], "/"),
			})

			p.routeProxy(c)
		})
	}

	return p
}

func (p *Proxy) Run() chan struct{} {
	p.Serve()
	finished := make(chan struct{})
	go func() {
		if err := p.engine.Run(":8082"); err != nil {
			panic(err)
		}
		finished <- struct{}{}
	}()

	return finished
}

func (p *Proxy) v2Endpoint(c *gin.Context) {
	err := p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false, nil)
	if err != nil {
		klog.Errorf("could not proxy registry: %s", err)
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
}

func (p *Proxy) routeProxy(c *gin.Context) {
	repository := c.Param("repository")
	originRegistry := c.Param("originRegistry")

	klog.InfoS("proxying request", "repository", repository, "originRegistry", originRegistry)

	if err := p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false, nil); err != nil {
		klog.InfoS("cached image is not available, proxying origin", "originRegistry", originRegistry, "error", err)

		transport, err := p.getAuthentifiedTransport(originRegistry, repository)
		if err != nil {
			_ = c.AbortWithError(http.StatusUnauthorized, err)
			return
		}

		if strings.HasSuffix(originRegistry, "docker.io") {
			originRegistry = "index.docker.io"
		}

		err = p.proxyRegistry(c, "https://"+originRegistry, true, transport)
		if err != nil {
			klog.Errorf("could not proxy registry: %s", err)
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}
}

func (p *Proxy) proxyRegistry(c *gin.Context, endpoint string, endpointIsOrigin bool, transport http.RoundTripper) error {
	klog.V(2).InfoS("proxying registry", "endpoint", endpoint)

	remote, err := url.Parse(endpoint)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)

	var proxyError error

	if transport != nil {
		proxy.Transport = transport
	}

	proxy.Director = func(req *http.Request) {
		req.Header = c.Request.Header
		req.Host = remote.Host
		req.URL.Scheme = remote.Scheme
		req.URL.Host = remote.Host

		// In the cache registry, images are prefixed with their origin registry.
		// Thus, when proxying the cache, we need to keep the origin part, but we have to discard it when proxying the origin
		pathParts := strings.Split(req.URL.Path, "/")
		if endpointIsOrigin && len(pathParts) > 2 {
			req.URL.Path = "/v2/" + strings.Join(pathParts[3:], "/")
		}

		// To prevent "X-Forwarded-For: 127.0.0.1, 127.0.0.1" which produce a HTTP 400 error
		req.Header.Del("X-Forwarded-For")

		if transport == nil {
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

func (p *Proxy) getAuthentifiedTransport(registryDomain string, repository string) (http.RoundTripper, error) {
	repositoryLabel := registry.RepositoryLabel(registryDomain + "/" + repository)
	cachedImages := &kuikenixiov1alpha1.CachedImageList{}

	klog.InfoS("listing CachedImages", "repositoryLabel", repositoryLabel)
	if err := p.k8sClient.List(context.Background(), cachedImages, client.MatchingLabels{
		kuikenixiov1alpha1.RepositoryLabelName: repositoryLabel,
	}, client.Limit(1)); err != nil {
		return nil, err
	}

	if len(cachedImages.Items) == 0 {
		return nil, errors.New("no CachedImage found for this repository")
	}

	cachedImage := cachedImages.Items[0] // Images from the same repository should need the same pull-secret
	if len(cachedImage.Spec.PullSecretNames) == 0 {
		return nil, nil // Not an error since not all images requires authentication to be pulled
	}

	keychain := registry.NewKubernetesKeychain(p.k8sClient, cachedImage.Spec.PullSecretsNamespace, cachedImage.Spec.PullSecretNames)

	ref, err := name.ParseReference(cachedImage.Spec.SourceImage)
	if err != nil {
		return nil, err
	}

	auth, err := keychain.Resolve(ref.Context())
	if err != nil {
		return nil, err
	}

	return transport.NewWithContext(context.Background(), ref.Context().Registry, auth, http.DefaultTransport, []string{ref.Scope(transport.PullScope)})
}

// See https://github.com/golang/go/issues/28239, https://github.com/golang/go/issues/23643 and https://github.com/golang/go/issues/56228
func recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); ok {
					if errors.Is(err, http.ErrAbortHandler) {
						return
					}
				}
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

func handleOriginRegistryPort(originRegistry string) string {
	re := regexp.MustCompile(`-([0-9]+)`)
	parts := re.FindStringSubmatch(originRegistry)

	if len(parts) == 2 {
		originRegistry = strings.ReplaceAll(originRegistry, parts[0], ":"+parts[1])
	}

	return originRegistry
}
