// Package registrytest serves in-memory registries over TLS, with certificates
// signed by a private test CA, for the specs of the registry clients' callers.
package registrytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

// T is the part of testing.T (and of GinkgoT()) the helpers use.
type T interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// CA is a private certificate authority.
type CA struct {
	// PEM is the CA certificate, as a CA Secret holds it.
	PEM  []byte
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func NewCA(t T) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kuik test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &CA{PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert: cert, key: key}
}

func (ca *CA) serverCertificate(t T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "kuik test registry"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// Registry is an in-memory registry served over TLS on 127.0.0.1: it is
// reachable only when its CA is trusted or its host is insecure. A second
// plain HTTP server on the same storage sets up and inspects images, whatever
// the TLS settings under test.
type Registry struct {
	server *httptest.Server
	setup  *httptest.Server
}

// NewTLSRegistry starts a registry whose certificate ca signs.
func NewTLSRegistry(t T, ca *CA) *Registry {
	t.Helper()
	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	r := &Registry{server: httptest.NewUnstartedServer(handler), setup: httptest.NewServer(handler)}
	r.server.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverCertificate(t)}}
	r.server.StartTLS()
	t.Cleanup(r.server.Close)
	t.Cleanup(r.setup.Close)
	return r
}

// Host is the registry host, 127.0.0.1:port.
func (r *Registry) Host() string {
	return r.server.Listener.Addr().String()
}

func (r *Registry) setupHost() string {
	return strings.TrimPrefix(r.setup.URL, "http://")
}

// Push stores a linux/amd64 image under repository and returns its digest.
func (r *Registry) Push(t T, repository string) string {
	t.Helper()
	image, err := random.Image(256, 1)
	if err != nil {
		t.Fatalf("random image: %v", err)
	}
	config, err := image.ConfigFile()
	if err != nil {
		t.Fatalf("config file: %v", err)
	}
	config.OS = "linux"
	config.Architecture = "amd64"
	if image, err = mutate.ConfigFile(image, config); err != nil {
		t.Fatalf("mutate config file: %v", err)
	}
	if err := crane.Push(image, r.setupHost()+"/"+repository); err != nil {
		t.Fatalf("push %s: %v", repository, err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatalf("digest of %s: %v", repository, err)
	}
	return digest.String()
}

// Has reports whether the registry stores repository (a tag or a digest).
func (r *Registry) Has(repository string) bool {
	_, err := crane.Head(r.setupHost() + "/" + repository)
	return err == nil
}
