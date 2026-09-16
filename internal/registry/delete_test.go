package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

const testRegistryAPIPath = "/v2/"

func TestDeleteImagePreservesSecureRegistryTransport(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var tlsDeletes atomic.Int64
	var plainDeletes atomic.Int64

	manifestHandler := func(label string, deleteCounter *atomic.Int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if label == "TLS" && r.URL.Path == testRegistryAPIPath {
				time.Sleep(500 * time.Millisecond)
			}
			w.Header().Set("Content-Length", "0")
			switch {
			case r.URL.Path == testRegistryAPIPath:
				w.WriteHeader(http.StatusOK)
			case r.Method == http.MethodHead:
				w.Header().Set("Docker-Content-Digest", digest)
				w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
				w.WriteHeader(http.StatusOK)
			case r.Method == http.MethodDelete:
				deleteCounter.Add(1)
				w.WriteHeader(http.StatusAccepted)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
	}

	plainServer := httptest.NewServer(manifestHandler("plain", &plainDeletes))
	t.Cleanup(plainServer.Close)

	tlsServer := httptest.NewTLSServer(manifestHandler("TLS", &tlsDeletes))
	t.Cleanup(tlsServer.Close)

	tlsHost, tlsPort, err := net.SplitHostPort(strings.TrimPrefix(tlsServer.URL, "https://"))
	if err != nil {
		t.Fatalf("split TLS server address: %v", err)
	}
	registryHost := fmt.Sprintf("registry.test:%s", tlsPort)
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(tlsServer.Certificate())

	plainAddress := strings.TrimPrefix(plainServer.URL, "http://")
	tlsAddress := strings.TrimPrefix(tlsServer.URL, "https://")
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	baseTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, plainAddress)
	}
	baseTransport.DialTLSContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, tlsAddress)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, &tls.Config{RootCAs: rootCAs, ServerName: tlsHost})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = baseTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	originalFallback := newFallbackKeychain
	newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
		return anonymousKeychain{}, nil
	}
	t.Cleanup(func() { newFallbackKeychain = originalFallback })

	client := NewClient(nil, rootCAs)
	if err := client.DeleteImage(context.Background(), registryHost+"/test/image:tag"); err != nil {
		t.Fatalf("DeleteImage failed: %v", err)
	}

	if got := tlsDeletes.Load(); got != 1 {
		t.Fatalf("TLS DELETE requests = %d, want 1", got)
	}
	if got := plainDeletes.Load(); got != 0 {
		t.Fatalf("plain HTTP DELETE requests = %d, want 0", got)
	}

	plainDeletes.Store(0)
	client = NewClient(nil, nil)
	if err := client.DeleteImage(context.Background(), plainAddress+"/test/image:tag"); err != nil {
		t.Fatalf("DeleteImage for localhost failed: %v", err)
	}
	if got := plainDeletes.Load(); got != 1 {
		t.Fatalf("localhost HTTP DELETE requests = %d, want 1", got)
	}
}
