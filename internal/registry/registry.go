// Package registry reads and writes OCI registries: availability checks, verbatim copies,
// tag listings and tag deletions.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/prometheus/client_golang/prometheus"
)

// RequestsTotal counts the requests kuik sent to a source registry. The process that reads
// source registries registers it.
var RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "kuik_registry_requests_total",
	Help: "Requests kuik sent to a source registry, by operation (Check, Copy) and outcome " +
		"(Ok, or the reason that request produced: ManifestNotFound, Unauthorized, QuotaExceeded, Unreachable)",
}, []string{"registry", "operation", "result"})

const resultOk = "Ok"

// retryStatusCodes overrides go-containerregistry's defaults, which retry 429 since
// v0.21.9. A rate limit must surface at once as QuotaExceeded: a transport-level retry
// would only spend more of an exhausted quota.
var retryStatusCodes = []int{
	http.StatusRequestTimeout,
	http.StatusInternalServerError,
	http.StatusBadGateway,
	http.StatusServiceUnavailable,
	http.StatusGatewayTimeout,
	499, // nginx-specific, client closed request
	522, // Cloudflare-specific, connection timeout
}

// Endpoint is one reference kuik reads or writes, with what it takes to reach it.
type Endpoint struct {
	// Reference is an image reference for Check and a Copy source, a repository for a Copy
	// destination and ListTags, a tag for DeleteTag.
	Reference string
	// Insecure reaches the registry over plain HTTP.
	Insecure bool
	// Auth are the credentials to try, in order, until one answers. None means anonymous.
	Auth []authn.Authenticator
}

func (e Endpoint) nameOptions() []name.Option {
	if e.Insecure {
		return []name.Option{name.Insecure}
	}
	return nil
}

// Client talks to registries. One Client is safe for concurrent use: nothing a call
// observes is kept on it.
type Client struct {
	transport http.RoundTripper
}

// NewClient returns a Client.
func NewClient() *Client {
	return &Client{transport: http.DefaultTransport.(*http.Transport).Clone()}
}

// WithTimeout bounds ctx by timeout, or leaves it unbounded when timeout is 0.
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timed out after %v", timeout))
}

// attempt is one try of an operation with one credential.
type attempt struct {
	auth    authn.Authenticator
	options []remote.Option
	headers *headerCapture
}

// try runs do once per credential of auths, in order, and stops at the first success. With
// no credential it runs do once, anonymously: anonymous is what is left when nothing else
// is declared, never a fallback after a refused credential. It returns the credential that
// answered, or the error of every attempt in order. The caller's context bounds the whole
// loop.
func (c *Client) try(ctx context.Context, auths []authn.Authenticator, do func(attempt) error) (authn.Authenticator, []error) {
	return c.tryThrough(ctx, c.transport, auths, do)
}

// tryThrough is try with the requests sent through base.
func (c *Client) tryThrough(ctx context.Context, base http.RoundTripper, auths []authn.Authenticator,
	do func(attempt) error) (authn.Authenticator, []error) {
	if len(auths) == 0 {
		auths = []authn.Authenticator{authn.Anonymous}
	}
	var errs []error
	for _, auth := range auths {
		headers := &headerCapture{next: base}
		err := do(attempt{
			auth:    auth,
			headers: headers,
			options: []remote.Option{
				remote.WithContext(ctx),
				remote.WithAuth(auth),
				remote.WithTransport(headers),
				remote.WithRetryStatusCodes(retryStatusCodes...),
			},
		})
		if err == nil {
			return auth, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errs
}

// registryLabel names the host of ref as kuik's configuration does: docker.io, not
// index.docker.io.
func registryLabel(ref name.Reference) string {
	host := ref.Context().RegistryStr()
	if host == name.DefaultRegistry {
		return "docker.io"
	}
	return host
}

// TransportStatusCode extracts the HTTP status code of a registry transport error, wrapped
// or joined. It returns 0 when err is not a transport error.
func TransportStatusCode(err error) int {
	if transportErr, ok := errors.AsType[*transport.Error](err); ok {
		return transportErr.StatusCode
	}
	return 0
}
