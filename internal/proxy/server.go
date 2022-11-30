package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/docker/distribution/reference"
	"github.com/gin-gonic/gin"
	dcrenixiov1alpha1 "gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Proxy struct {
	engine    *gin.Engine
	k8sClient client.Client
}

var errorMissingDockerConfigJsonInPullSecret = errors.New("pull secret is missing key .dockerconfigjson")

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

func (p *Proxy) Listen() *Proxy {
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
				c.Error(err)
				return
			}
			image := ref.String()

			c.Request.URL.Path = fmt.Sprintf("/v2/%s/%s", image, subMatches[2])

			imageParts := strings.Split(image, "/")
			c.Params = append(c.Params, gin.Param{
				Key:   "originRegistry",
				Value: imageParts[0],
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

func (p *Proxy) Serve() chan struct{} {
	finished := make(chan struct{})
	go func() {
		p.engine.Run(":8082")
		finished <- struct{}{}
	}()

	return finished
}

func (p *Proxy) v2Endpoint(c *gin.Context) {
	p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false, "")
}

func (p *Proxy) routeProxy(c *gin.Context) {
	repository := c.Param("repository")
	originRegistry := c.Param("originRegistry")

	klog.InfoS("proxying request", "repository", repository, "originRegistry", originRegistry)

	if err := p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false, ""); err != nil {
		klog.InfoS("cached image is not available, proxying origin", "originRegistry", originRegistry, "error", err)

		basicAuth, err := p.getBasicAuth(originRegistry, repository)
		if err != nil {
			c.AbortWithError(http.StatusUnauthorized, err)
			return
		}

		if strings.HasSuffix(originRegistry, "docker.io") {
			originRegistry = "index.docker.io"
		}
		p.proxyRegistry(c, "https://"+originRegistry, true, basicAuth)
	}
}

func (p *Proxy) proxyRegistry(c *gin.Context, endpoint string, endpointIsOrigin bool, basicAuth string) error {
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

		// In the cache registry, images are prefixed with their origin registry.
		// Thus, when proxying the cache, we need to keep the origin part, but we have to discard it when proxying the origin
		pathParts := strings.Split(req.URL.Path, "/")
		if endpointIsOrigin && len(pathParts) > 2 {
			req.URL.Path = "/v2/" + strings.Join(pathParts[3:], "/")
		}

		// To prevent "X-Forwarded-For: 127.0.0.1, 127.0.0.1" which produce a HTTP 400 error
		req.Header.Del("X-Forwarded-For")

		if basicAuth != "" {
			req.Header.Set("Authorization", "Basic "+basicAuth)
			return
		}

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

func (p *Proxy) getBasicAuth(registryDomain string, repository string) (string, error) {
	repositoryLabel := registry.SanitizeName(registryDomain + "/" + repository)
	cachedImages := &dcrenixiov1alpha1.CachedImageList{}
	secret := &corev1.Secret{}

	klog.InfoS("listing CachedImages", "repositoryLabel", repositoryLabel)
	if err := p.k8sClient.List(context.Background(), cachedImages, client.MatchingLabels{
		dcrenixiov1alpha1.RepositoryLabelName: repositoryLabel,
	}, client.Limit(1)); err != nil {
		return "", err
	}

	if len(cachedImages.Items) == 0 {
		return "", errors.New("no CachedImage found for this repository")
	}

	cachedImage := cachedImages.Items[0] // Images from the same repository should need the same pull-secret
	if len(cachedImage.Spec.PullSecretNames) == 0 {
		return "", nil // Not an error since not all images requires authentication to be pulled
	}

	// TODO: support multiple pull secrets
	objectKey := client.ObjectKey{Name: cachedImage.Spec.PullSecretNames[0], Namespace: cachedImage.Spec.PullSecretsNamespace}
	if err := p.k8sClient.Get(context.Background(), objectKey, secret); err != nil {
		return "", err
	}

	return getBasicAuthFromSecret(registryDomain, secret)
}

func getBasicAuthFromSecret(registryDomain string, secret *corev1.Secret) (string, error) {
	dockerConfigJson, exists := secret.Data[".dockerconfigjson"]
	if !exists {
		return "", errorMissingDockerConfigJsonInPullSecret
	}

	dockerConfig := struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}{}

	json.Unmarshal(dockerConfigJson, &dockerConfig)

	auth, ok := dockerConfig.Auths[registryDomain]
	if !ok {
		return "", nil
	}

	return auth.Auth, nil
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
