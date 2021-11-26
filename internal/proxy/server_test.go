package proxy

import (
	"testing"
)

func TestRewriteToInternalUrl(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantPath   string
		wantOrigin string
	}{
		{
			name:       "Empty path",
			path:       "",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "Empty path with trailing slash",
			path:       "/",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint",
			path:       "/v2",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint with trailing slash",
			path:       "/v2/",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "v2 endpoint with trailing slash",
			path:       "/v2/docker.io/library",
			wantPath:   "",
			wantOrigin: "",
		},
		{
			name:       "From standard library",
			path:       "/v2/docker.io/library/nginx/manifests/xxxxx",
			wantPath:   "/internal/library/nginx/manifests/xxxxx",
			wantOrigin: "docker.io",
		},
		{
			name:       "From user library",
			path:       "/v2/docker.io/enix/san-iscsi-csi/manifests/xxxxx",
			wantPath:   "/internal/enix/san-iscsi-csi/manifests/xxxxx",
			wantOrigin: "docker.io",
		},
		{
			name:       "From custom registry with tag and digest",
			path:       "/v2/quay.io/prometheus/busybox/manifests/glibc@sha256:9c2d6d09bbc625f07587d321f4b2aec88e44ae752804ba905b048c8bba1b3025",
			wantPath:   "/internal/prometheus/busybox/manifests/glibc@sha256:9c2d6d09bbc625f07587d321f4b2aec88e44ae752804ba905b048c8bba1b3025",
			wantOrigin: "quay.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, origin := RewriteToInternalUrl(tt.path)
			if path != tt.wantPath {
				t.Errorf("RewriteToInternalUrl() = (%v, _) want (%v, _)", path, tt.wantPath)
			}
			if origin != tt.wantOrigin {
				t.Errorf("RewriteToInternalUrl() = (_, %v) want (_, %v)", origin, tt.wantOrigin)
			}
		})
	}
}
