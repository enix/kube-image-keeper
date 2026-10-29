package registry

import (
	"net/http"
	"sync"
	"testing"
)

type headerRoundTripper struct{}

func (headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Ratelimit-Remaining": []string{req.URL.Path}},
		Request:    req,
	}, nil
}

// Concurrent requests through one HeaderCapture must not race on the captured
// headers: CopyImage uploads layers in parallel through a single transport.
func TestHeaderCaptureConcurrentRoundTrips(t *testing.T) {
	hc := &HeaderCapture{roundTripper: headerRoundTripper{}}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				req, err := http.NewRequest(http.MethodGet, "http://registry.test/v2/library/image/manifests/latest", nil)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := hc.RoundTrip(req); err != nil {
					t.Error(err)
					return
				}
				hc.GetLastHeaders()
			}
		}()
	}
	wg.Wait()
}
