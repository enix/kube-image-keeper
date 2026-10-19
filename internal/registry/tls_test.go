package registry

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/google/go-containerregistry/pkg/crane"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

const testRepository = "test/image:latest"

// testCA is a private certificate authority that signs the certificates of the
// test registries.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
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
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// serverCertificate issues a certificate for 127.0.0.1 and the given names.
func (ca *testCA) serverCertificate(t *testing.T, dnsNames ...string) tls.Certificate {
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
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (ca *testCA) bundle(source string) CABundle {
	return CABundle{Source: source, PEM: ca.pem}
}

// entry is a tls entry that trusts the CA.
func (ca *testCA) entry() TLSEntry {
	bundle := ca.bundle("test CA")
	return TLSEntry{CA: &bundle}
}

// pool is a trust store holding the CA alone.
func (ca *testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

// testRegistry is an in-memory registry served over TLS (or plain HTTP), with
// a second plain HTTP server on the same storage to set up and inspect images
// without going through the TLS settings under test.
type testRegistry struct {
	handler  http.Handler
	server   *httptest.Server
	setup    *httptest.Server
	requests atomic.Int64
	// connections counts the connections the registry accepted.
	connections atomic.Int64
	// closedConnections counts the connections that were closed.
	closedConnections atomic.Int64
	// manifestRedirect, when set, redirects every manifest request to this
	// base URL (scheme://host:port).
	manifestRedirect atomic.Pointer[string]
	manifestReads    atomic.Int64
	// tokenRealm, when set, requires the bearer token testToken that the token
	// server at this URL hands out.
	tokenRealm atomic.Pointer[string]
}

const testToken = "test-token"

// newTestRegistry starts a registry whose certificate ca signs for 127.0.0.1
// and dnsNames. A nil ca serves plain HTTP.
func newTestRegistry(t *testing.T, ca *testCA, dnsNames ...string) *testRegistry {
	t.Helper()
	r := &testRegistry{handler: ggcrregistry.New(ggcrregistry.Logger(log.New(io.Discard, "", 0)))}
	r.server = httptest.NewUnstartedServer(http.HandlerFunc(r.serveHTTP))
	r.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			r.connections.Add(1)
		case http.StateClosed:
			r.closedConnections.Add(1)
		}
	}
	if ca == nil {
		r.server.Start()
	} else {
		r.server.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverCertificate(t, dnsNames...)}}
		r.server.StartTLS()
	}
	t.Cleanup(r.server.Close)
	r.setup = httptest.NewServer(r.handler)
	t.Cleanup(r.setup.Close)
	return r
}

func (r *testRegistry) serveHTTP(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	if realm := r.tokenRealm.Load(); realm != nil && req.Header.Get("Authorization") != "Bearer "+testToken {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q,service="test"`, *realm))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.Contains(req.URL.Path, "/manifests/") {
		r.manifestReads.Add(1)
		if redirect := r.manifestRedirect.Load(); redirect != nil {
			http.Redirect(w, req, *redirect+req.URL.Path, http.StatusTemporaryRedirect)
			return
		}
	}
	r.handler.ServeHTTP(w, req)
}

// newTokenServer starts a token server, over TLS signed by ca for dnsName,
// that hands out testToken to anyone.
func newTokenServer(t *testing.T, ca *testCA, dnsName string) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"token":%q}`, testToken)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverCertificate(t, dnsName)}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// host is the loopback address of the registry, host:port.
func (r *testRegistry) host() string {
	return r.server.Listener.Addr().String()
}

// port is the port of the registry, to build a host name the test dialer
// resolves to it.
func (r *testRegistry) port() string {
	_, port, _ := net.SplitHostPort(r.host())
	return port
}

func (r *testRegistry) setupHost() string {
	return strings.TrimPrefix(r.setup.URL, "http://")
}

// push stores an image under repository on the registry and returns its digest.
func (r *testRegistry) push(t *testing.T, repository string) string {
	t.Helper()
	image := testImage(t)
	if err := crane.Push(image, r.setupHost()+"/"+repository); err != nil {
		t.Fatalf("push %s: %v", repository, err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatalf("digest of %s: %v", repository, err)
	}
	return digest.String()
}

// has reports whether the registry stores repository.
func (r *testRegistry) has(t *testing.T, repository string) bool {
	t.Helper()
	_, err := crane.Head(r.setupHost() + "/" + repository)
	return err == nil
}

// testImage is a linux/amd64 image, so that a copy finds its platform.
func testImage(t *testing.T) v1.Image {
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
	image, err = mutate.ConfigFile(image, config)
	if err != nil {
		t.Fatalf("mutate config file: %v", err)
	}
	return image
}

// testDialer keeps every connection on the machine: a *.test host dials
// 127.0.0.1 on the same port, an address of aliases dials its target, any
// other host but 127.0.0.1 is refused.
func testDialer(aliases map[string]string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if target, ok := aliases[addr]; ok {
			return dialer.DialContext(ctx, network, target)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.HasSuffix(host, ".test"):
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		case host == "127.0.0.1":
			return dialer.DialContext(ctx, network, addr)
		default:
			return nil, fmt.Errorf("test dialer: unexpected address %s", addr)
		}
	}
}

// settings returns TLS settings that dial through testDialer.
func settings(hosts map[string]TLSEntry, aliases map[string]string) *TLSSettings {
	return &TLSSettings{Hosts: hosts, dialContext: testDialer(aliases)}
}

// skipVerify is an insecureSkipVerify entry.
var skipVerify = TLSEntry{InsecureSkipVerify: true}

// newConnectProxy starts an HTTPS proxy, over TLS signed by ca for dnsNames
// (proxy.test by default), that tunnels CONNECT requests to 127.0.0.1; a *.test target goes there too.
func newConnectProxy(t *testing.T, ca *testCA, dnsNames ...string) *httptest.Server {
	if len(dnsNames) == 0 {
		dnsNames = []string{"proxy.test"}
	}
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		host, port, err := net.SplitHostPort(req.Host)
		if err != nil || (!strings.HasSuffix(host, ".test") && host != "127.0.0.1") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		upstream, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() {
			_, _ = io.Copy(upstream, conn)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverCertificate(t, dnsNames...)}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// throughProxy sends every request of s through the proxy, named
// proxy.test, and trusts system as the system trust store.
func throughProxy(s *TLSSettings, proxy *httptest.Server, system *testCA) *TLSSettings {
	_, port, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	s.testHooks = &tlsTestHooks{
		proxy:       http.ProxyURL(&url.URL{Scheme: "https", Host: "proxy.test:" + port}),
		systemRoots: func() (*x509.CertPool, error) { return system.pool(), nil },
	}
	return s
}

func checkAvailability(t *testing.T, s *TLSSettings, reference string) (kuikv1alpha1.ImageAvailabilityStatus, error) {
	t.Helper()
	return CheckImageAvailability(context.Background(), reference, http.MethodGet, 5*time.Second, nil, false, s)
}

func expectStatus(t *testing.T, s *TLSSettings, reference string, want kuikv1alpha1.ImageAvailabilityStatus) {
	t.Helper()
	_ = expectStatusError(t, s, reference, want)
}

// expectStatusError checks the status and returns the error that comes with it.
func expectStatusError(t *testing.T, s *TLSSettings, reference string, want kuikv1alpha1.ImageAvailabilityStatus) error {
	t.Helper()
	got, err := checkAvailability(t, s, reference)
	if got != want {
		t.Fatalf("%s: status = %s, want %s (error: %v)", reference, got, want, err)
	}
	return err
}

func TestClientTLS(t *testing.T) {
	t.Run("a self-signed registry is unreachable without a tls entry", func(t *testing.T) {
		unknown := newTestRegistry(t, newTestCA(t))
		unknown.push(t, testRepository)
		other := newTestRegistry(t, newTestCA(t))
		other.push(t, testRepository)
		s := settings(map[string]TLSEntry{other.host(): skipVerify}, nil)

		expectStatus(t, s, other.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, s, unknown.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("insecureSkipVerify reaches a self-signed registry", func(t *testing.T) {
		reg := newTestRegistry(t, newTestCA(t), "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()

		expectStatus(t, settings(map[string]TLSEntry{host: skipVerify}, nil), host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("insecureSkipVerify skips the verification of the certificate name", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca, "registry-a.test")
		reg.push(t, testRepository)
		wrongName := "registry-b.test:" + reg.port()

		verified := settings(map[string]TLSEntry{wrongName: ca.entry()}, nil)
		expectStatus(t, verified, wrongName+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)

		skipped := settings(map[string]TLSEntry{wrongName: skipVerify}, nil)
		expectStatus(t, skipped, wrongName+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a private CA in caSecretRef is trusted for its host", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca, "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()

		expectStatus(t, settings(map[string]TLSEntry{host: ca.entry()}, nil), host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a host CA does not apply to another host", func(t *testing.T) {
		ca := newTestCA(t)
		regA := newTestRegistry(t, ca, "registry-a.test")
		regA.push(t, testRepository)
		regB := newTestRegistry(t, ca, "registry-b.test")
		regB.push(t, testRepository)
		hostA := "registry-a.test:" + regA.port()
		s := settings(map[string]TLSEntry{hostA: ca.entry()}, nil)

		expectStatus(t, s, hostA+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, s, "registry-b.test:"+regB.port()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("a host key matches the port exactly", func(t *testing.T) {
		ca := newTestCA(t)
		regA := newTestRegistry(t, ca, "registry-a.test")
		regA.push(t, testRepository)
		regB := newTestRegistry(t, ca, "registry-b.test")
		regB.push(t, testRepository)
		hostB := "registry-b.test:" + regB.port()
		s := settings(map[string]TLSEntry{"registry-a.test": ca.entry(), hostB: ca.entry()}, nil)

		expectStatus(t, s, hostB+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, s, "registry-a.test:"+regA.port()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("a host key applies to the host in any case", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca, "registry.test")
		reg.push(t, testRepository)
		s := settings(map[string]TLSEntry{"registry.test:" + reg.port(): ca.entry()}, nil)

		expectStatus(t, s, "REGISTRY.test:"+reg.port()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a docker.io key applies to index.docker.io requests", func(t *testing.T) {
		reg := newTestRegistry(t, newTestCA(t))
		reg.push(t, "library/test:latest")
		s := settings(map[string]TLSEntry{"docker.io": skipVerify}, map[string]string{
			"index.docker.io:443": reg.host(),
		})

		expectStatus(t, s, "docker.io/library/test:latest", kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a plain HTTP registry on loopback is reachable without a tls entry or with insecureSkipVerify", func(t *testing.T) {
		// go-containerregistry's rule: plain HTTP for localhost, loopback and
		// private addresses
		reg := newTestRegistry(t, nil)
		reg.push(t, testRepository)

		expectStatus(t, settings(nil, nil), reg.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, settings(map[string]TLSEntry{reg.host(): skipVerify}, nil), reg.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a plain HTTP registry on loopback is unreachable with a caSecretRef, before any request", func(t *testing.T) {
		// a declared CA says the host speaks verified TLS
		reg := newTestRegistry(t, nil)
		reg.push(t, testRepository)
		s := settings(map[string]TLSEntry{reg.host(): newTestCA(t).entry()}, nil)

		err := expectStatusError(t, s, reg.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		if err == nil || !strings.Contains(err.Error(), "refusing plain HTTP to "+reg.host()) {
			t.Errorf("expected plain HTTP to be refused naming the host, got %v", err)
		}
		if requests := reg.requests.Load(); requests != 0 {
			t.Errorf("expected no request to be sent, got %d", requests)
		}
	})

	t.Run("a plain HTTP registry on a DNS name is unreachable even with insecureSkipVerify", func(t *testing.T) {
		reg := newTestRegistry(t, nil)
		reg.push(t, testRepository)
		host := "plain.test:" + reg.port()

		expectStatus(t, settings(map[string]TLSEntry{host: skipVerify}, nil), host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("a redirect follows the tls entry of the host it goes to", func(t *testing.T) {
		caA := newTestCA(t)
		regA := newTestRegistry(t, caA)
		regB := newTestRegistry(t, newTestCA(t))
		regB.push(t, testRepository)
		regA.manifestRedirect.Store(&regB.server.URL)
		s := settings(map[string]TLSEntry{regA.host(): caA.entry(), regB.host(): skipVerify}, nil)

		expectStatus(t, s, regA.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		if reads := regB.manifestReads.Load(); reads == 0 {
			t.Fatal("expected the manifest to be read from the redirect target")
		}
	})

	t.Run("a redirect to a host without a tls entry does not inherit the registry entry", func(t *testing.T) {
		caA, caB := newTestCA(t), newTestCA(t)
		regA := newTestRegistry(t, caA)
		regB := newTestRegistry(t, caB)
		regB.push(t, testRepository)
		regA.manifestRedirect.Store(&regB.server.URL)
		both := CABundle{Source: "CAs A and B", PEM: append(append([]byte{}, caA.pem...), caB.pem...)}
		s := settings(map[string]TLSEntry{regA.host(): {CA: &both}}, nil)

		expectStatus(t, s, regA.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		if reads := regA.manifestReads.Load(); reads == 0 {
			t.Fatal("expected the registry to be reached before the redirect")
		}
		if reads := regB.manifestReads.Load(); reads != 0 {
			t.Fatalf("expected the redirect target to refuse the TLS handshake, got %d manifest reads", reads)
		}
	})

	t.Run("a token server gets the tls entry of its own host", func(t *testing.T) {
		registryCA, tokenCA := newTestCA(t), newTestCA(t)
		reg := newTestRegistry(t, registryCA, "registry.test")
		reg.push(t, testRepository)
		tokenServer := newTokenServer(t, tokenCA, "auth.test")
		_, tokenPort, _ := net.SplitHostPort(tokenServer.Listener.Addr().String())
		authHost := "auth.test:" + tokenPort
		realm := "https://" + authHost + "/token"
		reg.tokenRealm.Store(&realm)
		host := "registry.test:" + reg.port()

		own := settings(map[string]TLSEntry{host: registryCA.entry(), authHost: tokenCA.entry()}, nil)
		expectStatus(t, own, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)

		both := CABundle{Source: "registry and token CAs", PEM: append(append([]byte{}, registryCA.pem...), tokenCA.pem...)}
		onRegistry := settings(map[string]TLSEntry{host: {CA: &both}}, nil)
		expectStatus(t, onRegistry, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("a copy uses the tls entries of the source and of the destination", func(t *testing.T) {
		destCA := newTestCA(t)
		src := newTestRegistry(t, newTestCA(t), "source.test")
		src.push(t, testRepository)
		dest := newTestRegistry(t, destCA, "destination.test")
		srcHost := "source.test:" + src.port()
		destHost := "destination.test:" + dest.port()
		s := settings(map[string]TLSEntry{srcHost: skipVerify, destHost: destCA.entry()}, nil)

		client := NewClient().WithTLS(s)
		desc, err := client.GetDescriptor(srcHost + "/" + testRepository)
		if err != nil {
			t.Fatalf("get source descriptor: %v", err)
		}
		platforms := []v1.Platform{{OS: "linux", Architecture: "amd64"}}
		if err := client.CopyImage(context.Background(), desc, destHost+"/mirror/"+testRepository, platforms); err != nil {
			t.Fatalf("copy: %v", err)
		}
		if !dest.has(t, "mirror/"+testRepository) {
			t.Fatal("expected the image in the destination")
		}
	})

	t.Run("a deletion uses the tls entry of the registry", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca, "registry.test")
		digest := reg.push(t, testRepository)
		host := "registry.test:" + reg.port()

		if err := NewClient().WithTLS(settings(map[string]TLSEntry{host: ca.entry()}, nil)).DeleteImage(host + "/" + testRepository); err != nil {
			t.Fatalf("delete: %v", err)
		}
		// DeleteImage deletes the manifest by digest; the in-memory registry
		// keeps the tag entry, so check the digest
		if reg.has(t, "test/image@"+digest) {
			t.Fatal("expected the image to be deleted")
		}
	})

	t.Run("a deletion of a plain HTTP registry on a DNS name fails at its HEAD", func(t *testing.T) {
		reg := newTestRegistry(t, nil)
		digest := reg.push(t, testRepository)
		host := "plain.test:" + reg.port()

		if err := NewClient().WithTLS(settings(map[string]TLSEntry{host: skipVerify}, nil)).DeleteImage(host + "/" + testRepository); err == nil {
			t.Fatal("expected the deletion to fail")
		}
		if !reg.has(t, "test/image@"+digest) {
			t.Fatal("expected the image to remain")
		}
	})

}

func TestClientTLSProxy(t *testing.T) {
	t.Run("an HTTPS proxy hop is verified against the system trust store, not the host CA", func(t *testing.T) {
		system, hostCA := newTestCA(t), newTestCA(t)
		reg := newTestRegistry(t, hostCA, "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()
		reference := host + "/" + testRepository

		trusted := newConnectProxy(t, system)
		expectStatus(t, throughProxy(settings(map[string]TLSEntry{host: hostCA.entry()}, nil), trusted, system), reference, kuikv1alpha1.ImageAvailabilityAvailable)

		byHostCA := newConnectProxy(t, hostCA)
		expectStatus(t, throughProxy(settings(map[string]TLSEntry{host: hostCA.entry()}, nil), byHostCA, system), reference, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("an HTTPS proxy hop is verified for an insecureSkipVerify host", func(t *testing.T) {
		system := newTestCA(t)
		reg := newTestRegistry(t, newTestCA(t), "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()
		reference := host + "/" + testRepository

		trusted := newConnectProxy(t, system)
		expectStatus(t, throughProxy(settings(map[string]TLSEntry{host: skipVerify}, nil), trusted, system), reference, kuikv1alpha1.ImageAvailabilityAvailable)

		untrusted := newConnectProxy(t, newTestCA(t))
		expectStatus(t, throughProxy(settings(map[string]TLSEntry{host: skipVerify}, nil), untrusted, system), reference, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("refuses an HTTPS proxy addressed by IP in front of a registry addressed by IP", func(t *testing.T) {
		// TLS sends no server name to an IP address: the proxy hop could not
		// be told from the hop to the registry, and would skip verification
		reg := newTestRegistry(t, newTestCA(t))
		reg.push(t, testRepository)
		proxy := newConnectProxy(t, newTestCA(t))
		s := settings(map[string]TLSEntry{reg.host(): skipVerify}, nil)
		s.testHooks = &tlsTestHooks{proxy: http.ProxyURL(&url.URL{Scheme: "https", Host: proxy.Listener.Addr().String()})}

		err := expectStatusError(t, s, reg.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		if err == nil || !strings.Contains(err.Error(), "refusing the HTTPS proxy") {
			t.Errorf("expected the proxy to be refused, got %v", err)
		}
		if requests := reg.requests.Load(); requests != 0 {
			t.Errorf("expected no request to reach the registry, got %d", requests)
		}
	})

	t.Run("refuses an HTTPS proxy addressed by IP in front of a registry addressed by DNS name with a tls entry", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca, "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()
		proxy := newConnectProxy(t, newTestCA(t))
		s := settings(map[string]TLSEntry{host: ca.entry()}, nil)
		s.testHooks = &tlsTestHooks{proxy: http.ProxyURL(&url.URL{Scheme: "https", Host: proxy.Listener.Addr().String()})}

		err := expectStatusError(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		if err == nil || !strings.Contains(err.Error(), "refusing the HTTPS proxy") {
			t.Errorf("expected the proxy to be refused, got %v", err)
		}
		if requests := reg.requests.Load(); requests != 0 {
			t.Errorf("expected no request to reach the registry, got %d", requests)
		}
	})

	t.Run("refuses an HTTPS proxy that shares the registry's host name", func(t *testing.T) {
		// the proxy hop would pass the server name test as the registry hop
		// and skip verification; http.Transport sends host names in IDNA
		// ASCII, so the names compare in that form
		tests := []struct {
			name          string
			registryName  string
			proxyHostname string
		}{
			{name: "in another case", registryName: "registry.test", proxyHostname: "REGISTRY.test"},
			{name: "in Unicode for an IDNA ASCII registry", registryName: "xn--bcher-kva.test", proxyHostname: "bücher.test"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				reg := newTestRegistry(t, newTestCA(t), tt.registryName)
				reg.push(t, testRepository)
				host := tt.registryName + ":" + reg.port()
				proxy := newConnectProxy(t, newTestCA(t))
				_, proxyPort, _ := net.SplitHostPort(proxy.Listener.Addr().String())
				s := settings(map[string]TLSEntry{host: skipVerify}, nil)
				s.testHooks = &tlsTestHooks{proxy: http.ProxyURL(&url.URL{Scheme: "https", Host: tt.proxyHostname + ":" + proxyPort})}

				err := expectStatusError(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
				if err == nil || !strings.Contains(err.Error(), "refusing the HTTPS proxy") {
					t.Errorf("expected the proxy to be refused, got %v", err)
				}
				if requests := reg.requests.Load(); requests != 0 {
					t.Errorf("expected no request to reach the registry, got %d", requests)
				}
			})
		}
	})

	t.Run("refuses an HTTPS proxy whose host name has no IDNA ASCII form", func(t *testing.T) {
		reg := newTestRegistry(t, newTestCA(t), "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()
		s := settings(map[string]TLSEntry{host: skipVerify}, nil)
		s.testHooks = &tlsTestHooks{proxy: http.ProxyURL(&url.URL{Scheme: "https", Host: "proxy\u00ad\u200d-.test:8443"})}

		err := expectStatusError(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		if err == nil || !strings.Contains(err.Error(), "refusing the HTTPS proxy") {
			t.Errorf("expected the proxy to be refused, got %v", err)
		}
	})

	t.Run("accepts an HTTPS proxy whose ASCII host name IDNA would reject", func(t *testing.T) {
		// net/http sends an ASCII host name as is, without IDNA
		system := newTestCA(t)
		reg := newTestRegistry(t, newTestCA(t), "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()
		proxy := newConnectProxy(t, system, "proxy_a.test")
		_, proxyPort, _ := net.SplitHostPort(proxy.Listener.Addr().String())
		s := settings(map[string]TLSEntry{host: skipVerify}, nil)
		s.testHooks = &tlsTestHooks{
			proxy:       http.ProxyURL(&url.URL{Scheme: "https", Host: "proxy_a.test:" + proxyPort}),
			systemRoots: func() (*x509.CertPool, error) { return system.pool(), nil },
		}

		expectStatus(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

}

func TestClientTLSRouting(t *testing.T) {
	t.Run("an empty tls entry verifies the host against the system trust store, as no entry does", func(t *testing.T) {
		reg := newTestRegistry(t, newTestCA(t), "registry.test")
		reg.push(t, testRepository)
		host := "registry.test:" + reg.port()

		expectStatus(t, settings(map[string]TLSEntry{host: {}}, nil), host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
	})

	t.Run("a redirect to a host in uppercase gets the tls entry of that host", func(t *testing.T) {
		caA, caB := newTestCA(t), newTestCA(t)
		regA := newTestRegistry(t, caA)
		regB := newTestRegistry(t, caB, "registry-b.test")
		regB.push(t, testRepository)
		redirect := "https://REGISTRY-B.test:" + regB.port()
		regA.manifestRedirect.Store(&redirect)
		s := settings(map[string]TLSEntry{regA.host(): caA.entry(), "registry-b.test:" + regB.port(): caB.entry()}, nil)

		expectStatus(t, s, regA.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
	})

	t.Run("a rotated CA replaces the transport of the host and closes its idle connections", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca)
		reg.push(t, testRepository)
		reference := reg.host() + "/" + testRepository
		// no test dialer: the transports of a dialer are not shared
		withCAs := func(cas ...*testCA) *TLSSettings {
			var pemData []byte
			for _, c := range cas {
				pemData = append(pemData, c.pem...)
			}
			return &TLSSettings{Hosts: map[string]TLSEntry{reg.host(): {CA: &CABundle{Source: "test CAs", PEM: pemData}}}}
		}

		expectStatus(t, withCAs(ca), reference, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, withCAs(ca, newTestCA(t)), reference, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, withCAs(ca, newTestCA(t)), reference, kuikv1alpha1.ImageAvailabilityAvailable)

		// each rotation opens a connection and closes the idle one of the
		// replaced transport: only the last transport keeps a connection
		deadline := time.Now().Add(5 * time.Second)
		for reg.closedConnections.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if opened, closed := reg.connections.Load(), reg.closedConnections.Load(); opened != 3 || closed != 2 {
			t.Fatalf("expected 3 connections opened and 2 closed, got %d opened and %d closed", opened, closed)
		}
	})

	t.Run("clients with equal settings reuse the connections", func(t *testing.T) {
		ca := newTestCA(t)
		reg := newTestRegistry(t, ca)
		reg.push(t, testRepository)
		reference := reg.host() + "/" + testRepository

		// no test dialer: the transports of a dialer are not shared
		expectStatus(t, &TLSSettings{Hosts: map[string]TLSEntry{reg.host(): ca.entry()}}, reference, kuikv1alpha1.ImageAvailabilityAvailable)
		expectStatus(t, &TLSSettings{Hosts: map[string]TLSEntry{reg.host(): ca.entry()}}, reference, kuikv1alpha1.ImageAvailabilityAvailable)

		if connections := reg.connections.Load(); connections != 1 {
			t.Fatalf("expected 1 connection, got %d", connections)
		}
	})
}

func TestCheckImageAvailabilityUnusableCA(t *testing.T) {
	// expectFailedBeforeSent checks that the registry is reported unreachable,
	// with an error that names the CA, and that it received no request.
	expectFailedBeforeSent := func(t *testing.T, reg *testRegistry, s *TLSSettings, wantInError ...string) {
		t.Helper()
		err := expectStatusError(t, s, reg.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
		for _, want := range wantInError {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("expected an error containing %q, got %v", want, err)
			}
		}
		if requests := reg.requests.Load(); requests != 0 {
			t.Errorf("expected no request to be sent, got %d", requests)
		}
	}

	// The registries serve plain HTTP on loopback, which go-containerregistry
	// reaches whatever the TLS settings: only the CA error can stop the request.
	t.Run("a CA that could not be read makes the registry unreachable before any request, naming its source", func(t *testing.T) {
		reg := newTestRegistry(t, nil)
		reg.push(t, testRepository)
		notFound := CABundle{Source: "Secret kuik-system/harbor-ca", Err: errors.New(`secrets "harbor-ca" not found`)}

		expectFailedBeforeSent(t, reg, settings(map[string]TLSEntry{reg.host(): {CA: &notFound}}, nil), "Secret kuik-system/harbor-ca", `secrets "harbor-ca" not found`)
	})

	t.Run("an unusable host CA leaves other hosts reachable", func(t *testing.T) {
		broken := newTestRegistry(t, nil)
		broken.push(t, testRepository)
		ca := newTestCA(t)
		healthy := newTestRegistry(t, ca)
		healthy.push(t, testRepository)
		notFound := CABundle{Source: "Secret kuik-system/harbor-ca", Err: errors.New(`secrets "harbor-ca" not found`)}
		s := settings(map[string]TLSEntry{broken.host(): {CA: &notFound}, healthy.host(): ca.entry()}, nil)

		expectStatus(t, s, healthy.host()+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
		expectFailedBeforeSent(t, broken, s)
	})
}

func TestCABundlePEM(t *testing.T) {
	first, second := newTestCA(t), newTestCA(t)
	block := func(blockType string, data []byte) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data})
	}
	concat := func(parts ...[]byte) []byte {
		var out []byte
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}

	// a usable bundle reaches a registry signed by second; an unusable one
	// stops every request before it is sent
	tests := []struct {
		name   string
		bundle []byte
		usable bool
	}{
		{name: "a bundle with several certificates trusts each of them", bundle: concat(first.pem, second.pem), usable: true},
		{name: "text outside the PEM blocks is ignored", bundle: concat([]byte("subject=kuik test CA\n"), second.pem, []byte("trailing comment\n")), usable: true},
		{name: "an empty bundle is unusable", bundle: nil},
		{name: "text without a PEM block is unusable", bundle: []byte("not a certificate")},
		{name: "a truncated PEM block is unusable", bundle: concat(second.pem, first.pem[:len(first.pem)/2])},
		// cut on a line boundary: pem.Decode then skips the broken block and
		// decodes the next one
		{name: "a truncated PEM block followed by a complete one is unusable", bundle: concat(bytes.Join(bytes.SplitAfter(first.pem, []byte("\n"))[:3], nil), second.pem)},
		{name: "a PEM block that is not a CERTIFICATE is unusable", bundle: concat(second.pem, block("PRIVATE KEY", []byte("key")))},
		{name: "a certificate that does not parse is unusable", bundle: concat(second.pem, block("CERTIFICATE", []byte("garbage")))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(t, second, "registry.test")
			reg.push(t, testRepository)
			host := "registry.test:" + reg.port()
			bundle := CABundle{Source: "Secret kuik-system/harbor-ca, key ca.crt", PEM: tt.bundle}
			s := settings(map[string]TLSEntry{host: {CA: &bundle}}, nil)

			if tt.usable {
				expectStatus(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityAvailable)
				return
			}
			err := expectStatusError(t, s, host+"/"+testRepository, kuikv1alpha1.ImageAvailabilityUnreachable)
			if err == nil || !strings.Contains(err.Error(), "Secret kuik-system/harbor-ca, key ca.crt") {
				t.Errorf("expected an error naming the bundle, got %v", err)
			}
			if requests := reg.requests.Load(); requests != 0 {
				t.Errorf("expected no request to be sent, got %d", requests)
			}
		})
	}
}
