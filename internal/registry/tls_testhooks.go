package registry

import (
	"crypto/x509"
	"net/http"
	"net/url"
)

// tlsTestHooks replace parts of the transports of TLSSettings, for tests only.
type tlsTestHooks struct {
	// proxy replaces the proxy of the transports.
	proxy func(*http.Request) (*url.URL, error)
	// systemRoots replaces the system trust store.
	systemRoots func() (*x509.CertPool, error)
}
