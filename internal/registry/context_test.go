package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestExecutePropagatesCanceledContext(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	originalFallback := newFallbackKeychain
	t.Cleanup(func() { newFallbackKeychain = originalFallback })

	var fallbackContext context.Context
	newFallbackKeychain = func(ctx context.Context) (authn.Keychain, error) {
		fallbackContext = ctx
		return anonymousKeychain{}, nil
	}

	client := NewClient([]string{strings.TrimPrefix(server.URL, "http://")}, nil)
	err := client.Execute(ctx, strings.TrimPrefix(server.URL, "http://")+"/image:tag", func(ref name.Reference, opts ...remote.Option) error {
		_, err := remote.Head(ref, opts...)
		return err
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
	if fallbackContext == nil || !errors.Is(fallbackContext.Err(), context.Canceled) {
		t.Fatalf("fallback keychain context = %v, want canceled context", fallbackContext)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("registry requests = %d, want 0 for canceled context", got)
	}
}

type anonymousKeychain struct{}

func (anonymousKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return authn.Anonymous, nil
}
