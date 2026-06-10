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

// brokenDigestRegistry wraps an in-memory registry and can be set to return
// 404 for manifest requests by digest while keeping tag requests working.
// This reproduces a pull-through proxy with a stale tag cache: tags keep
// resolving (HEAD and GET return 200 with a Docker-Content-Digest header),
// but the manifests behind those digests are gone, so container runtimes
// fail to pull on any node that does not already have the image.
type brokenDigestRegistry struct {
	handler       http.Handler
	brokenDigests atomic.Bool
	manifestReads atomic.Int64
}

func (b *brokenDigestRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/manifests/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		b.manifestReads.Add(1)
		if b.brokenDigests.Load() && strings.Contains(r.URL.Path, "/manifests/sha256:") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`)
			return
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
		brokenDigests bool
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
			name:          "broken digests without digest resolution stay invisible",
			reference:     tagReference,
			method:        http.MethodHead,
			brokenDigests: true,
			want:          kuikv1alpha1.ImageAvailabilityAvailable,
			wantReads:     1,
		},
		{
			name:          "broken digests with digest resolution HEAD are detected",
			reference:     tagReference,
			method:        http.MethodHead,
			brokenDigests: true,
			resolveDigest: true,
			want:          kuikv1alpha1.ImageAvailabilityNotFound,
			wantErr:       "tag/digest inconsistency",
			wantReads:     2,
		},
		{
			name:          "broken digests with digest resolution GET are detected",
			reference:     tagReference,
			method:        http.MethodGet,
			brokenDigests: true,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.brokenDigests.Store(tt.brokenDigests)
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
				t.Errorf("manifest read requests = %d, want %d", reads, tt.wantReads)
			}
		})
	}
}
