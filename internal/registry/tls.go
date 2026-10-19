package registry

import (
	"context"
	"net"
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

	// Default and Items are the previous schema, until the router moves to Hosts.
	Default TLSEntry
	Items   map[string]TLSEntry
}

// TLSEntry are the TLS settings of one host.
type TLSEntry struct {
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool
	// CA, when set, is added to the system trust store for this host only.
	CA *CABundle

	// Insecure and CAs are the previous schema, until the router moves to CA.
	Insecure bool
	CAs      []CABundle
}

// CABundle is a PEM bundle of CA certificates, or the error met reading it.
type CABundle struct {
	// Source names where the bundle comes from, for error messages.
	Source string
	PEM    []byte
	Err    error
}

// WithTLS sets the TLS settings of the client. Stub: only the test dialer is used.
func (c *Client) WithTLS(settings *TLSSettings) *Client {
	c.tls = settings
	return c
}
