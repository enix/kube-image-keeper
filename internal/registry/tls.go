package registry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
)

// TLSHost is how kuik verifies the certificate of one registry host.
type TLSHost struct {
	// CABundle is a PEM bundle of certificates added to the system trust store.
	CABundle []byte
	// CASource names where the bundle comes from, in every error about it.
	CASource string
	// CAError is why the bundle could not be read. Every request to the host fails with it,
	// before it is sent.
	CAError error
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool
	// Pending is a bundle not read yet: a request to the host waits until the settings are
	// replaced, bounded by its context. See PendingTLSHost.
	Pending bool
}

// clientOptions are what tests replace in a Client.
type clientOptions struct {
	// dial replaces the dialer of every transport.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// systemRoots replaces the system trust store.
	systemRoots func() (*x509.CertPool, error)
}

// notSentError is a request the client refused to send. The transport that sees it returned
// knows the request was not sent; an error tree does not tell, go-containerregistry joining
// the errors of parallel requests.
type notSentError struct{ err error }

func (e *notSentError) Error() string { return e.err.Error() }
func (e *notSentError) Unwrap() error { return e.err }

// sent reports whether a RoundTrip that returned err sent its request.
func sent(err error) bool {
	_, refused := err.(*notSentError) //nolint:errorlint // the error a RoundTrip returns, never wrapped
	return !refused
}

// SetTLS replaces the TLS settings, keyed by host as the global config's `tls` key is:
// host[:port] exactly, docker.io for Docker Hub. A host without an entry is verified against
// the system trust store. A host whose settings did not change keeps its connections; the
// others drop theirs.
func (c *Client) SetTLS(hosts map[string]TLSHost) {
	c.router.set(hosts)
}

// route is the transport of one host with a tls entry, or why none can be built.
type route struct {
	settings  TLSHost
	transport *http.Transport
	err       error
}

func (r *route) sameSettings(h TLSHost) bool {
	return r.settings.InsecureSkipVerify == h.InsecureSkipVerify &&
		r.settings.Pending == h.Pending &&
		bytes.Equal(r.settings.CABundle, h.CABundle) &&
		r.settings.CASource == h.CASource &&
		fmt.Sprint(r.settings.CAError) == fmt.Sprint(h.CAError)
}

// tlsRouter sends each request through the transport of the host it goes to, so that a
// redirect (blob storage, CDN) or a token server follows the entry of its own host.
type tlsRouter struct {
	base        *http.Transport
	systemRoots func() (*x509.CertPool, error)

	mu     sync.RWMutex
	routes map[string]*route
	// replaced is closed, and a new one made, each time set replaces the routes: what a
	// pending request waits on.
	replaced chan struct{}
}

func newTLSRouter(opts clientOptions) *tlsRouter {
	base := http.DefaultTransport.(*http.Transport).Clone()
	if opts.dial != nil {
		base.DialContext = opts.dial
	}
	roots := opts.systemRoots
	if roots == nil {
		roots = x509.SystemCertPool
	}
	return &tlsRouter{base: base, systemRoots: roots, routes: map[string]*route{}, replaced: make(chan struct{})}
}

func (r *tlsRouter) set(hosts map[string]TLSHost) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*route, len(hosts))
	for host, settings := range hosts {
		host = strings.ToLower(host)
		if previous, ok := r.routes[host]; ok && previous.sameSettings(settings) {
			next[host] = previous
			continue
		}
		next[host] = r.build(host, settings)
	}
	for host, previous := range r.routes {
		if next[host] != previous && previous.transport != nil {
			previous.transport.CloseIdleConnections()
		}
	}
	r.routes = next
	close(r.replaced)
	r.replaced = make(chan struct{})
}

// build makes the transport of the entry of host. An unusable CA leaves no transport: never
// fall back to a weaker verification. crypto/tls's own verification is replaced by
// connectionVerifier, which applies the entry to the registry hop alone.
func (r *tlsRouter) build(host string, settings TLSHost) *route {
	rt := &route{settings: settings}
	if settings.Pending {
		return rt
	}
	if settings.CAError != nil {
		rt.err = fmt.Errorf("%s: %w", settings.CASource, settings.CAError)
		return rt
	}
	if settings.CABundle != nil {
		if _, err := ParseCABundle(settings.CABundle); err != nil {
			rt.err = fmt.Errorf("%s: %w", settings.CASource, err)
			return rt
		}
	}
	rt.transport = r.base.Clone()
	rt.transport.Proxy = refuseIPProxy(r.base.Proxy)
	rt.transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection verifies every hop
		VerifyConnection:   connectionVerifier(host, settings, r.systemRoots),
	}
	return rt
}

// lookup returns the route of host, nil for a host without an entry. A pending route is
// waited out, until set replaces it or ctx is done.
func (r *tlsRouter) lookup(ctx context.Context, host string) (*route, error) {
	for {
		r.mu.RLock()
		rt, replaced := r.routes[tlsKey(host)], r.replaced
		r.mu.RUnlock()
		if rt == nil || !rt.settings.Pending {
			return rt, nil
		}
		select {
		case <-replaced:
		case <-ctx.Done():
			return nil, fmt.Errorf("%s: not read yet: %w", rt.settings.CASource, context.Cause(ctx))
		}
	}
}

// dockerHubKey names Docker Hub in kuik's configuration, whose requests go to
// index.docker.io.
const dockerHubKey = "docker.io"

// tlsKey names a request host as the `tls` key does: lowercased, docker.io for the host
// Docker Hub requests go to.
func tlsKey(host string) string {
	host = strings.ToLower(host)
	if host == name.DefaultRegistry {
		return dockerHubKey
	}
	return host
}

func (r *tlsRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	rt, err := r.lookup(req.Context(), req.URL.Host)
	if rt == nil && err == nil {
		return r.base.RoundTrip(req)
	}
	if err == nil {
		err = rt.err
	}
	if err == nil && req.URL.Scheme != "https" {
		// go-containerregistry falls back to plain HTTP on loopback and private addresses. A
		// host with a tls entry speaks TLS: only a CR entry declared insecure reaches it in clear.
		err = fmt.Errorf("refusing plain HTTP to %s: it has a tls entry", tlsKey(req.URL.Host))
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &notSentError{err}
	}
	return rt.transport.RoundTrip(req)
}

// insecureRoute reaches one host, the registry of a CR entry declared insecure, through the
// plain transport whatever its tls entry holds, and every other host through the router.
type insecureRoute struct {
	host   string
	plain  http.RoundTripper
	router http.RoundTripper
}

func (i *insecureRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Host, i.host) {
		return i.plain.RoundTrip(req)
	}
	return i.router.RoundTrip(req)
}

// transportFor returns the transport the requests of an endpoint on registry go through.
func (c *Client) transportFor(registry name.Registry, insecure bool) http.RoundTripper {
	if !insecure {
		return c.router
	}
	return &insecureRoute{host: registry.RegistryStr(), plain: c.router.base, router: c.router}
}
