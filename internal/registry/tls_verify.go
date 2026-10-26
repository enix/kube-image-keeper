package registry

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
)

// errNoCertificate is a CA bundle that is not a list of PEM certificates.
var errNoCertificate = errors.New("the bundle must hold PEM certificates only, and at least one")

// ParseCABundle reads a PEM bundle of CA certificates. Text outside the blocks is ignored, as
// containerd and x509.CertPool.AppendCertsFromPEM do with comments. A truncated block, a block
// that is not a certificate or a certificate that does not parse rejects the whole bundle, as
// does a bundle with no certificate.
func ParseCABundle(bundle []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: found a %s block", errNoCertificate, block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errNoCertificate, err)
		}
		certs = append(certs, cert)
	}
	// pem.Decode skips a truncated block when a complete one follows it: count the blocks the
	// bundle opens rather than look for a leftover one.
	if opened := bytes.Count(bundle, []byte("-----BEGIN")); opened != len(certs) {
		return nil, fmt.Errorf("%w: truncated PEM block, %d of %d blocks decoded", errNoCertificate, len(certs), opened)
	}
	if len(certs) == 0 {
		return nil, errNoCertificate
	}
	return certs, nil
}

// connectionVerifier verifies the connections of the transport of host, which carries the
// TLS of an HTTPS proxy hop too. Only the hop whose server name is host's follows the entry;
// any other is verified against the system trust store, as without an entry. Go sends no
// server name for an IP address: a host written as an IP is the hop without one, and a proxy
// written as an IP in front of a host written as a name cannot be verified, so it is refused.
func connectionVerifier(host string, settings TLSHost, systemRoots func() (*x509.CertPool, error)) func(tls.ConnectionState) error {
	hostname := requestHostname(host)
	sni := hostname
	if net.ParseIP(hostname) != nil {
		sni = ""
	}
	system, systemErr := systemRoots()
	if systemErr != nil {
		systemErr = fmt.Errorf("loading the system trust store: %w", systemErr)
	}
	roots, rootsErr := entryRoots(settings, systemRoots)

	return func(cs tls.ConnectionState) error {
		// Host names are case-insensitive: a redirect or a realm may write one in uppercase.
		registryHop := strings.EqualFold(cs.ServerName, sni)
		switch {
		case !registryHop && cs.ServerName == "":
			return fmt.Errorf("cannot verify a TLS hop addressed by IP on the way to %s", hostname)
		case !registryHop:
			if systemErr != nil {
				return systemErr
			}
			return verifyChain(cs, system, cs.ServerName)
		case settings.InsecureSkipVerify:
			return nil
		case rootsErr != nil:
			return rootsErr
		default:
			return verifyChain(cs, roots, hostname)
		}
	}
}

// entryRoots is the system trust store with the certificates of the entry's bundle.
func entryRoots(settings TLSHost, systemRoots func() (*x509.CertPool, error)) (*x509.CertPool, error) {
	pool, err := systemRoots()
	if err != nil {
		return nil, fmt.Errorf("loading the system trust store: %w", err)
	}
	if settings.CABundle == nil {
		return pool, nil
	}
	certs, err := ParseCABundle(settings.CABundle)
	if err != nil {
		return nil, err
	}
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
}

// requestHostname is the host name requests to a `tls` key go to: no port, and
// index.docker.io for docker.io.
func requestHostname(key string) string {
	if key == dockerHubKey {
		return "index.docker.io"
	}
	if h, _, err := net.SplitHostPort(key); err == nil {
		return h
	}
	return strings.Trim(key, "[]")
}

// verifyChain verifies the peer chain of cs against roots for name, as crypto/tls does.
func verifyChain(cs tls.ConnectionState, roots *x509.CertPool, name string) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("the server sent no certificate")
	}
	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		DNSName:       name,
		Intermediates: intermediates,
	})
	return err
}
