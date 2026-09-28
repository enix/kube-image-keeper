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

// isRateLimited reports whether the rate-limit headers of a response say the quota is
// exhausted ("ratelimit-remaining: 0;w=...").
func isRateLimited(headers http.Header) bool {
	remaining := headers.Get("RateLimit-Remaining")
	return remaining == "0" || strings.HasPrefix(remaining, "0;")
}
