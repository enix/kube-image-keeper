package registry

import (
	"crypto/tls"
	"crypto/x509"
)

// connectionVerifier is a stub.
func connectionVerifier(string, TLSHost, func() (*x509.CertPool, error)) func(tls.ConnectionState) error {
	return func(tls.ConnectionState) error { return nil }
}
