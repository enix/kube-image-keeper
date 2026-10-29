package registry

import (
	"net/http"
	"sync"
)

// HeaderCapture wraps an http.RoundTripper to capture response headers
type HeaderCapture struct {
	roundTripper http.RoundTripper
	mu           sync.Mutex
	lastHeaders  http.Header
}

func (hc *HeaderCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := hc.roundTripper.RoundTrip(req)
	if resp != nil && resp.Header != nil {
		headers := resp.Header.Clone()
		hc.mu.Lock()
		hc.lastHeaders = headers
		hc.mu.Unlock()
	}
	return resp, err
}

func (hc *HeaderCapture) GetLastHeaders() http.Header {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	return hc.lastHeaders
}
