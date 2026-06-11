package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
)

// digestResponseMode controls how brokenDigestRegistry responds to manifest-by-digest requests.
type digestResponseMode int

const (
	digestResponseNormal           digestResponseMode = iota // pass through to the real registry
	digestResponseNotFound                                   // 404 — stale tag cache / blocked by policy
	digestResponseRateLimit                                  // 429 with RateLimit-Remaining: 0 header
	digestResponseUnauthorized                               // 401 — fine-grained auth on digest path
	digestResponseMethodNotAllowed                           // 405 — proxy supports tags but not digest lookup
)

// brokenDigestRegistry wraps an in-memory registry and can intercept manifest-by-digest
// requests to simulate various proxy and registry failure modes while keeping tag requests
// working. Subtests must not run in parallel because digestMode is shared state.
type brokenDigestRegistry struct {
	handler       http.Handler
	digestMode    atomic.Int32 // stores digestResponseMode
	manifestReads atomic.Int64
}

func (b *brokenDigestRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/manifests/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		b.manifestReads.Add(1)
		if strings.Contains(r.URL.Path, "/manifests/sha256:") {
			switch digestResponseMode(b.digestMode.Load()) {
			case digestResponseNotFound:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`)
				return
			case digestResponseRateLimit:
				w.Header().Set("RateLimit-Remaining", "0;w=21600")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			case digestResponseUnauthorized:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`)
				return
			case digestResponseMethodNotAllowed:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}
	}
	b.handler.ServeHTTP(w, r)
}

func TestCheckImageAvailabilityResolveDigest(t *testing.T) {
	fake := &brokenDigestRegistry{handler: registry.New()}
	server := httptest.NewServer(fake)
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	tagReference := host + "/test/image:latest"

	image, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(image, tagReference); err != nil {
		t.Fatal(err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	digestReference := host + "/test/image@" + digest.String()

	tests := []struct {
		name          string
		reference     string
		method        string
		digestMode    digestResponseMode
		resolveDigest bool
		want          kuikv1alpha1.ImageAvailabilityStatus
		wantErr       string
		wantReads     int64
	}{
		{
			name:      "healthy registry without digest resolution",
			reference: tagReference,
			method:    http.MethodHead,
			want:      kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads: 1,
		},
		{
			name:          "healthy registry with digest resolution HEAD",
			reference:     tagReference,
			method:        http.MethodHead,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:     2,
		},
		{
			name:          "healthy registry with digest resolution GET",
			reference:     tagReference,
			method:        http.MethodGet,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:     2,
		},
		{
			name:       "broken digests without digest resolution stay invisible",
			reference:  tagReference,
			method:     http.MethodHead,
			digestMode: digestResponseNotFound,
			want:       kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:  1,
		},
		{
			name:          "broken digests with digest resolution HEAD are detected",
			reference:     tagReference,
			method:        http.MethodHead,
			digestMode:    digestResponseNotFound,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityNotFound,
			wantErr:       "tag/digest inconsistency",
			wantReads:     2,
		},
		{
			name:          "broken digests with digest resolution GET are detected",
			reference:     tagReference,
			method:        http.MethodGet,
			digestMode:    digestResponseNotFound,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityNotFound,
			wantErr:       "tag/digest inconsistency",
			wantReads:     2,
		},
		{
			name:          "digest reference is not checked twice",
			reference:     digestReference,
			method:        http.MethodHead,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:     1,
		},
		{
			name:          "rate limit on digest path returns QuotaExceeded",
			reference:     tagReference,
			method:        http.MethodHead,
			digestMode:    digestResponseRateLimit,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityQuotaExceeded,
			wantErr:       "rate limit exceeded",
			wantReads:     2,
		},
		{
			name:          "auth failure on digest path returns InvalidAuth",
			reference:     tagReference,
			method:        http.MethodHead,
			digestMode:    digestResponseUnauthorized,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityInvalidAuth,
			wantErr:       "authentication failed",
			wantReads:     2,
		},
		{
			name:          "digest method not allowed returns Available",
			reference:     tagReference,
			method:        http.MethodHead,
			digestMode:    digestResponseMethodNotAllowed,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.digestMode.Store(int32(tt.digestMode))
			readsBefore := fake.manifestReads.Load()

			method := tt.method
			if method == "" {
				method = http.MethodHead
			}
			got, err := CheckImageAvailability(context.Background(), tt.reference, method, 5*time.Second, nil, tt.resolveDigest)

			if got != tt.want {
				t.Errorf("status = %v, want %v (err: %v)", got, tt.want, err)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
				}
			}
			if reads := fake.manifestReads.Load() - readsBefore; reads != tt.wantReads {
				t.Errorf("manifest read requests = %d, want %d (ref=%s method=%s resolveDigest=%v)", reads, tt.wantReads, tt.reference, tt.method, tt.resolveDigest)
			}
		})
	}
}
