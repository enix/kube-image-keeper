package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/go-containerregistry/pkg/name"
	"golang.org/x/net/idna"
)

// TLSSettings are the TLS settings of the requests to registries, per host.
type TLSSettings struct {
	// Hosts are keyed by host[:port] as the tls configuration keys are:
	// lowercased, docker.io for Docker Hub. A host without an entry is
	// verified against the system trust store.
	Hosts map[string]TLSEntry

	// dialContext replaces the dialer of the transports, for tests only.
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// testHooks replace parts of the transports, for tests only.
	testHooks *tlsTestHooks
}

// TLSEntry are the TLS settings of one host.
type TLSEntry struct {
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool
	// CA, when set, is added to the system trust store for this host only.
	CA *CABundle
}

// CABundle is a PEM bundle of CA certificates, or the error met reading it.
type CABundle struct {
	// Source names where the bundle comes from, for error messages.
	Source string
	PEM    []byte
	Err    error
}

// WithTLS sets the TLS settings of the client. Nil verifies every host
// against the system trust store.
func (c *Client) WithTLS(settings *TLSSettings) *Client {
	c.tls = settings
	return c
}

// tlsKey names a request host as a tls key does: lowercased, docker.io for
// index.docker.io, the host Docker Hub images are requested from.
func tlsKey(host string) string {
	host = strings.ToLower(host)
	if host == name.DefaultRegistry {
		return "docker.io"
	}
	return host
}

// entryFor returns the entry of a request host, and whether it has one.
func (s *TLSSettings) entryFor(host string) (TLSEntry, bool) {
	if s == nil {
		return TLSEntry{}, false
	}
	entry, ok := s.Hosts[tlsKey(host)]
	return entry, ok && (entry.InsecureSkipVerify || entry.CA != nil)
}

// tlsRouter sends each request through the transport of its own host, so that a
// redirect (blob storage, CDN) or a token server gets the entry of the host it
// goes to, not the one of the registry.
type tlsRouter struct {
	settings *TLSSettings

	mu         sync.Mutex
	transports map[string]http.RoundTripper
	errs       map[string]error
}

func newTLSRouter(settings *TLSSettings) *tlsRouter {
	if settings == nil {
		settings = &TLSSettings{}
	}
	return &tlsRouter{
		settings:   settings,
		transports: map[string]http.RoundTripper{},
		errs:       map[string]error{},
	}
}

func (r *tlsRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	var transport http.RoundTripper
	err := r.refusePlainHTTP(req)
	if err == nil {
		transport, err = r.transportFor(req.URL.Host)
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return transport.RoundTrip(req)
}

// refusePlainHTTP refuses a plain HTTP request to a host whose entry has a
// CA: go-containerregistry falls back to plain HTTP on localhost, loopback and
// private addresses, but a declared CA says the host speaks verified TLS.
// A host with insecureSkipVerify or without an entry keeps that fallback.
func (r *tlsRouter) refusePlainHTTP(req *http.Request) error {
	if req.URL.Scheme != "http" {
		return nil
	}
	if entry, ok := r.settings.entryFor(req.URL.Host); ok && entry.CA != nil {
		return fmt.Errorf("refusing plain HTTP to %s: its tls entry has a caSecretRef, so it must be reached over verified HTTPS", strings.ToLower(req.URL.Host))
	}
	return nil
}

func (r *tlsRouter) transportFor(host string) (http.RoundTripper, error) {
	host = strings.ToLower(host)

	r.mu.Lock()
	defer r.mu.Unlock()

	if transport, ok := r.transports[host]; ok {
		return transport, nil
	}
	if err, ok := r.errs[host]; ok {
		return nil, err
	}

	transport, err := r.newTransport(host)
	if err != nil {
		err = fmt.Errorf("unusable TLS settings for %s: %w", host, err)
		r.errs[host] = err
		return nil, err
	}
	r.transports[host] = transport
	return transport, nil
}

func (r *tlsRouter) newTransport(host string) (http.RoundTripper, error) {
	entry, ok := r.settings.entryFor(host)

	key := "system"
	switch {
	case !ok:
	case entry.InsecureSkipVerify:
		key = "insecureSkipVerify"
	case entry.CA.Err != nil:
		return nil, fmt.Errorf("CA %s: %w", entry.CA.Source, entry.CA.Err)
	default:
		hash := sha256.Sum256(entry.CA.PEM)
		key = "ca:" + hex.EncodeToString(hash[:])
	}

	hooks := r.settings.testHooks
	build := func() (*http.Transport, error) {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if r.settings.dialContext != nil {
			transport.DialContext = r.settings.dialContext
		}
		if hooks != nil && hooks.proxy != nil {
			transport.Proxy = hooks.proxy
		}
		if !ok {
			return transport, nil
		}
		var pool *x509.CertPool
		if !entry.InsecureSkipVerify {
			var err error
			if pool, err = r.certPool(entry.CA); err != nil {
				return nil, err
			}
		}
		// the transport verifies itself, so that an HTTPS proxy hop gets the
		// system trust store whatever the entry of the host holds
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // verifyConnection verifies every hop
			VerifyConnection:   r.verifyConnection(host, entry.InsecureSkipVerify, pool),
		}
		refuseUnverifiableProxy(transport, host)
		return transport, nil
	}

	if r.settings.dialContext != nil || hooks != nil {
		// the test seams are not part of the key: never share their transports
		return build()
	}

	return sharedTransports.get(host, key, build)
}

// refuseUnverifiableProxy refuses, before dialing, an HTTPS proxy whose hop
// verifyConnection cannot tell from the hop to a host with a tls entry: a
// proxy addressed by IP (TLS sends it no server name) and a proxy sharing the
// host name of the host, compared in IDNA ASCII as http.Transport sends them.
// Either would get the entry of the host, and with it the Proxy-Authorization
// header.
func refuseUnverifiableProxy(transport *http.Transport, host string) {
	if transport.Proxy == nil {
		return
	}
	hostname := hostnameOf(host)
	if ascii, err := idnaASCII(hostname); err == nil {
		hostname = ascii
	}
	proxy := transport.Proxy
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		proxyURL, err := proxy(req)
		if err != nil || proxyURL == nil || proxyURL.Scheme != "https" {
			return proxyURL, err
		}
		if net.ParseIP(proxyURL.Hostname()) != nil {
			return nil, fmt.Errorf("refusing the HTTPS proxy %s in front of %s: a proxy addressed by IP cannot be verified for a host with a tls entry, address it by DNS name", proxyURL.Host, host)
		}
		// http.Transport sends host names in IDNA ASCII: compare them so
		proxyName, err := idnaASCII(proxyURL.Hostname())
		if err != nil {
			return nil, fmt.Errorf("refusing the HTTPS proxy %s in front of %s: its host name has no IDNA ASCII form: %w", proxyURL.Host, host, err)
		}
		if strings.EqualFold(proxyName, hostname) {
			return nil, fmt.Errorf("refusing the HTTPS proxy %s in front of %s: a proxy sharing the host name of a host with a tls entry cannot be verified apart from it", proxyURL.Host, host)
		}
		return proxyURL, nil
	}
}

// idnaASCII returns a host name as http.Transport sends it: as is when it is
// already ASCII, in IDNA ASCII otherwise.
func idnaASCII(name string) (string, error) {
	for i := 0; i < len(name); i++ {
		if name[i] >= utf8.RuneSelf {
			return idna.Lookup.ToASCII(name)
		}
	}
	return name, nil
}

// hostnameOf returns the host name of host[:port], without brackets.
func hostnameOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}

// verifyConnection verifies the certificate of each TLS hop of the transport
// of host: the hop to host itself with its entry (no verification for
// insecureSkipVerify, the system trust store plus its CA otherwise), any other
// hop (an HTTPS proxy) against the system trust store alone. A hop is told
// apart by the server name it was dialed with; an IP address sends none.
func (r *tlsRouter) verifyConnection(host string, skipVerify bool, pool *x509.CertPool) func(tls.ConnectionState) error {
	hostname := hostnameOf(host)
	targetSNI := hostname
	if net.ParseIP(hostname) != nil {
		targetSNI = ""
	}

	return func(cs tls.ConnectionState) error {
		dnsName := cs.ServerName
		roots := pool
		if strings.EqualFold(cs.ServerName, targetSNI) {
			if skipVerify {
				return nil
			}
			dnsName = hostname
		} else {
			if cs.ServerName == "" {
				return fmt.Errorf("cannot verify a TLS hop to an IP address on the way to %s", host)
			}
			system, err := r.systemPool()
			if err != nil {
				return err
			}
			roots = system
		}
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the server sent no certificate")
		}
		intermediates := x509.NewCertPool()
		for _, cert := range cs.PeerCertificates[1:] {
			intermediates.AddCert(cert)
		}
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
			DNSName:       dnsName,
			Roots:         roots,
			Intermediates: intermediates,
		})
		return err
	}
}

func (r *tlsRouter) systemPool() (*x509.CertPool, error) {
	roots := x509.SystemCertPool
	if hooks := r.settings.testHooks; hooks != nil && hooks.systemRoots != nil {
		roots = hooks.systemRoots
	}
	pool, err := roots()
	if err != nil {
		return nil, fmt.Errorf("could not load the system trust store: %w", err)
	}
	return pool, nil
}

// sharedTransports holds the transport of each request host across clients,
// so that the clients built for each check reuse the connections. A transport
// is kept with the key of what it depends on (insecureSkipVerify, or the CA
// bundle): a new key for the host, a rotated CA, replaces it and closes its
// idle connections.
var sharedTransports = &transportCache{hosts: map[string]hostTransport{}}

type transportCache struct {
	mu    sync.Mutex
	hosts map[string]hostTransport
}

type hostTransport struct {
	key       string
	transport *http.Transport
}

func (c *transportCache) get(host, key string, build func() (*http.Transport, error)) (*http.Transport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, ok := c.hosts[host]
	if ok && previous.key == key {
		return previous.transport, nil
	}
	transport, err := build()
	if err != nil {
		return nil, err
	}
	c.hosts[host] = hostTransport{key: key, transport: transport}
	if ok {
		previous.transport.CloseIdleConnections()
	}
	return transport, nil
}

// certPool adds the certificates of the bundle to the system trust store. An
// unusable bundle fails the pool: never fall back to a weaker verification.
func (r *tlsRouter) certPool(bundle *CABundle) (*x509.CertPool, error) {
	pool, err := r.systemPool()
	if err != nil {
		return nil, err
	}
	certs, err := parseCertificates(bundle.PEM)
	if err != nil {
		return nil, fmt.Errorf("CA %s: %w", bundle.Source, err)
	}
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
}

var errNoPEMCertificate = errors.New("no valid PEM certificate")

// parseCertificates reads every PEM block of a bundle. Text outside the blocks
// is ignored, as openssl does; a truncated block, a block that is not a
// CERTIFICATE or a certificate that does not parse rejects the bundle whole.
func parseCertificates(bundle []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: unexpected %s PEM block", errNoPEMCertificate, block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errNoPEMCertificate, err)
		}
		certs = append(certs, cert)
	}
	// pem.Decode skips a broken block and decodes the next one: a bundle with
	// more BEGIN markers than decoded blocks holds a truncated block
	if begins := bytes.Count(bundle, []byte("-----BEGIN")); begins != len(certs) {
		return nil, fmt.Errorf("%w: truncated PEM block, %d of %d blocks decoded", errNoPEMCertificate, len(certs), begins)
	}
	if len(certs) == 0 {
		return nil, errNoPEMCertificate
	}
	return certs, nil
}
