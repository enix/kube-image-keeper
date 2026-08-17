package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// rateLimitedRegistry answers the /v2/ ping and returns 429 on every manifest
// request, counting them.
type rateLimitedRegistry struct {
	manifestReads atomic.Int64
}

func (r *rateLimitedRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.Contains(req.URL.Path, "/manifests/") {
		r.manifestReads.Add(1)
		w.Header().Set("RateLimit-Remaining", "0;w=21600")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// A 429 must surface immediately: kuik reports it as QuotaExceeded and its
// monitoring scheduler backs off, so transport-level retries would only spend
// more of an exhausted quota. go-containerregistry retries 429 by default
// since v0.21.9; this pins the pre-v0.21.9 behavior.
func TestRateLimitedRequestIsNotRetried(t *testing.T) {
	fake := &rateLimitedRegistry{}
	server := httptest.NewServer(fake)
	defer server.Close()

	reference := strings.TrimPrefix(server.URL, "http://") + "/test/image:latest"
	client := NewClient([]string{strings.TrimPrefix(server.URL, "http://")}, nil)

	_, headers, err := client.ReadDescriptor(http.MethodHead, reference)
	if err == nil {
		t.Fatal("expected an error from the rate-limited registry, got nil")
	}
	if code := TransportStatusCode(err); code != http.StatusTooManyRequests {
		t.Errorf("transport status code = %d, want %d", code, http.StatusTooManyRequests)
	}
	if !IsRateLimited(headers) {
		t.Error("expected rate-limit headers to be captured")
	}
	if reads := fake.manifestReads.Load(); reads != 1 {
		t.Errorf("manifest read requests = %d, want 1 (429 must not be retried)", reads)
	}
}
