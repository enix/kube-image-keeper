package registry

import (
	"net/http"
	"strings"
	"sync"
)

// headerCapture records the headers of the manifest responses of one attempt. It lives as
// long as that attempt, so concurrent calls on one Client never read each other's headers.
type headerCapture struct {
	next http.RoundTripper

	mu   sync.Mutex
	last http.Header
}

func (h *headerCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := h.next.RoundTrip(req)
	if resp != nil && strings.Contains(req.URL.Path, "/manifests/") {
		h.mu.Lock()
		h.last = resp.Header.Clone()
		h.mu.Unlock()
	}
	return resp, err
}

func (h *headerCapture) headers() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// failureWatch records whether a request sent through it failed: no answer, or a status
// that is an error. Copy sends every source request through one, redirects to a blob store
// included, to tell a source failure from a destination one.
type failureWatch struct {
	next http.RoundTripper

	mu     sync.Mutex
	failed bool
}

func (f *failureWatch) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f.next.RoundTrip(req)
	// A 401 is the start of an auth handshake, not a failure: a refused credential
	// surfaces as the error of the request that carried it.
	if err != nil || (resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnauthorized) {
		f.mu.Lock()
		f.failed = true
		f.mu.Unlock()
	}
	return resp, err
}

func (f *failureWatch) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = false
}

func (f *failureWatch) hasFailed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed
}

// isRateLimited reports whether the rate-limit headers of a response say the quota is
// exhausted ("ratelimit-remaining: 0;w=...").
func isRateLimited(headers http.Header) bool {
	remaining := headers.Get("RateLimit-Remaining")
	return remaining == "0" || strings.HasPrefix(remaining, "0;")
}
