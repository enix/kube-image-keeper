package registry

import (
	"context"
	"crypto/x509"
	"net"
)

// TLSHost is how kuik verifies the certificate of one registry host.
type TLSHost struct {
	// CABundle is a PEM bundle of certificates added to the system trust store.
	CABundle []byte
	// CASource names where the bundle comes from, in every error about it.
	CASource string
	// CAError is why the bundle could not be read.
	CAError error
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool
}

// SetTLS is a stub.
func (c *Client) SetTLS(map[string]TLSHost) {}

// clientOptions are what tests replace in a Client.
type clientOptions struct {
	dial        func(ctx context.Context, network, addr string) (net.Conn, error)
	systemRoots func() (*x509.CertPool, error)
}

// newClient is a stub.
func newClient(clientOptions) *Client { return NewClient() }
