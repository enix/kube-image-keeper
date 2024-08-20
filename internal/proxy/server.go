package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	kuikv1alpha1 "github.com/adisplayname/kube-image-keeper/api/kuik/v1alpha1ext1"
	"github.com/adisplayname/kube-image-keeper/internal/metrics"
	"github.com/adisplayname/kube-image-keeper/internal/registry"
	"github.com/distribution/reference"
	"github.com/gin-gonic/gin"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"golang.org/x/exp/slices"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Proxy struct {
	engine             *gin.Engine
	k8sClient          client.Client
	collector          *Collector
	exporter           *metrics.Exporter
	insecureRegistries []string
	rootCAs            *x509.CertPool
}

func New(k8sClient client.Client, metricsAddr string, insecureRegistries []string, rootCAs *x509.CertPool) *Proxy {
	collector := NewCollector()
	return &Proxy{
		k8sClient:          k8sClient,
		engine:             gin.New(),
		collector:          collector,
		exporter:           metrics.New(collector, metricsAddr),
		insecureRegistries: insecureRegistries,
		rootCAs:            rootCAs,
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

	r.Use(
		gin.LoggerWithWriter(gin.DefaultWriter, "/readyz", "/healthz"),
		recoveryMiddleware(),
		func(c *gin.Context) {
			c.Next()
			registry := c.Param("originRegistry")
			if registry == "" {
				return
			}
			p.collector.IncHTTPCall(registry, c.Writer.Status(), c.GetBool("cacheHit"))
		},
	)

	r.GET("/readyz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

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
				c.Status(http.StatusNotFound)
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

func (p *Proxy) Run(proxyAddr string) chan struct{} {
	p.Serve()
	finished := make(chan struct{})
	go func() {
		if err := p.engine.Run(proxyAddr); err != nil {
			panic(err)
		}
		finished <- struct{}{}
		p.exporter.Shutdown()
	}()

	go func() {
		if err := p.exporter.ListenAndServe(); err != nil {
			panic(err)
		}
	}()

	return finished
}

// https://distribution.github.io/distribution/spec/api/#api-version-check
func (p *Proxy) v2Endpoint(c *gin.Context) {
	c.Header("Docker-Distribution-Api-Version", "registry/2.0")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(http.StatusOK, map[string]string{})
}

func (p *Proxy) routeProxy(c *gin.Context) {
	repositoryName := c.Param("repository")
	originRegistry := c.Param("originRegistry")

	klog.InfoS("proxying request", "repository", repositoryName, "originRegistry", originRegistry)

	if err := p.proxyRegistry(c, registry.Protocol+registry.Endpoint, false, nil); err != nil {
		klog.InfoS("cached image is not available, proxying origin", "originRegistry", originRegistry, "error", err)

		repository, err := p.getRepository(originRegistry, repositoryName)
		if err != nil {
			if statusError, isStatus := err.(*apierrors.StatusError); isStatus && statusError.ErrStatus.Code != 0 {
				_ = c.AbortWithError(int(statusError.ErrStatus.Code), err)
			} else {
				_ = c.AbortWithError(http.StatusInternalServerError, err)
			}
			return
		}

		transport, err := p.getAuthentifiedTransport(repository, "https://"+originRegistry)
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
		}

		return
	}

	c.Set("cacheHit", true)
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
		if endpoint == registry.Protocol+registry.Endpoint && !(resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusTemporaryRedirect) {
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

func (p *Proxy) getRepository(registryDomain string, repositoryName string) (*kuikv1alpha1.Repository, error) {
	sanitizedName := registry.SanitizeName(registryDomain + "/" + repositoryName)

	repository := &kuikv1alpha1.Repository{}
	if err := p.k8sClient.Get(context.Background(), types.NamespacedName{Name: sanitizedName}, repository); err != nil {
		return nil, err
	}

	return repository, nil
}

func (p *Proxy) getKeychains(repository *kuikv1alpha1.Repository) ([]authn.Keychain, error) {
	pullSecrets, err := repository.GetPullSecrets(p.k8sClient)
	if err != nil {
		return nil, err
	}

	return registry.GetKeychains(repository.Spec.Name, pullSecrets)
}

func (p *Proxy) getAuthentifiedTransport(repository *kuikv1alpha1.Repository, originRegistry string) (http.RoundTripper, error) {
	imageRef, err := name.ParseReference(repository.Spec.Name)
	if err != nil {
		return nil, err
	}

	keychains, err := p.getKeychains(repository)
	if err != nil {
		return nil, err
	}

	var proxyErrors []error
	for _, keychain := range keychains {
		transport, err := p.getAuthentifiedTransportWithKeychain(imageRef.Context(), keychain)
		if err != nil {
			proxyErrors = append(proxyErrors, err)
			continue
		}

		client := &http.Client{Transport: transport}

		// if :latest doesn't exist, it will respond with 404 so we still can know that this transport is well authentified (!= 401)
		resp, err := client.Head(originRegistry + "/v2/" + imageRef.Context().RepositoryStr() + "/manifests/latest")
		if err != nil {
			proxyErrors = append(proxyErrors, err)
		} else if resp.StatusCode != http.StatusUnauthorized {
			return transport, nil
		}
	}

	return nil, utilerrors.NewAggregate(proxyErrors)
}

func (p *Proxy) getAuthentifiedTransportWithKeychain(repository name.Repository, keychain authn.Keychain) (http.RoundTripper, error) {
	auth, err := keychain.Resolve(repository)
	if err != nil {
		return nil, err
	}

	originalTransport := http.DefaultTransport.(*http.Transport).Clone()
	originalTransport.TLSClientConfig = &tls.Config{RootCAs: p.rootCAs}
	if slices.Contains(p.insecureRegistries, repository.Registry.RegistryStr()) {
		originalTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return transport.NewWithContext(context.Background(), repository.Registry, auth, originalTransport, []string{repository.Scope(transport.PullScope)})
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
	re := regexp.MustCompile(`-([0-9]+)$`)
	parts := re.FindStringSubmatch(originRegistry)
	originRegistryBytes := []byte(originRegistry)

	if len(parts) == 2 {
		originRegistryBytes = re.ReplaceAll(originRegistryBytes, []byte(":$1"))
	}

	return string(originRegistryBytes)
}
