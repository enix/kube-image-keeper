package registry

import (
	"net/http"
)

// HeaderCapture wraps an http.RoundTripper to capture response headers
type HeaderCapture struct {
	roundTripper http.RoundTripper
	lastHeaders  http.Header
}

func (hc *HeaderCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := hc.roundTripper.RoundTrip(req)
	if resp != nil && resp.Header != nil {
		hc.lastHeaders = resp.Header.Clone()
	}
	return resp, err
}

func (hc *HeaderCapture) GetLastHeaders() http.Header {
	return hc.lastHeaders
}
