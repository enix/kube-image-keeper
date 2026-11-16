// Package testutil holds helpers shared by tests that talk to a server
// through go-containerregistry.
package testutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Transport dials srv's address whatever host the request names, so
// references like upstream.kt.test/acme/foo reach the test server.
func Transport(srv *httptest.Server) http.RoundTripper {
	addr := srv.Listener.Addr().String()
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
}

// Ref parses s as an insecure reference (plain HTTP allowed). It panics when s
// does not parse: fixtures are constants.
func Ref(s string) name.Reference {
	r, err := name.ParseReference(s, name.Insecure)
	if err != nil {
		panic(err)
	}
	return r
}

// Opts returns remote options that route every request to srv. Retries are
// off (one attempt, no backoff) so a faulted answer reaches the spec at once.
func Opts(srv *httptest.Server) []remote.Option {
	return []remote.Option{
		remote.WithTransport(Transport(srv)),
		remote.WithRetryBackoff(remote.Backoff{Steps: 1}),
	}
}

// ZotURL returns the URL of the zot registry the integration tests run
// against; the boolean is false when ZOT_URL is not set, and the caller skips.
func ZotURL() (string, bool) {
	zot := os.Getenv("ZOT_URL")
	return zot, zot != ""
}

// UniqueRepo returns a repository name no other test or run uses, so tests
// can share a long-lived backend. testName must be a valid repository path
// segment: callers pass a short constant naming the test.
func UniqueRepo(testName string) string {
	return fmt.Sprintf("acme/%s-%d", strings.ToLower(testName), time.Now().UnixNano())
}
