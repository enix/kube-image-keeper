package registry

import (
	"context"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestPlatformString(t *testing.T) {
	tests := []struct {
		name     string
		platform v1.Platform
		want     string
	}{
		{
			name:     "architecture only",
			platform: v1.Platform{Architecture: "amd64"},
			want:     "amd64",
		},
		{
			name:     "os and architecture",
			platform: v1.Platform{OS: "linux", Architecture: "amd64"},
			want:     "linux/amd64",
		},
		{
			name:     "os, architecture and variant",
			platform: v1.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			want:     "linux/arm64/v8",
		},
		{
			name:     "architecture and variant without os",
			platform: v1.Platform{Architecture: "arm", Variant: "v7"},
			want:     "arm/v7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := platformString(tt.platform); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestMatchPlatforms(t *testing.T) {
	var (
		amd64 = v1.Platform{OS: "linux", Architecture: "amd64"}
		arm64 = v1.Platform{OS: "linux", Architecture: "arm64"}
	)

	tests := []struct {
		name        string
		configured  []v1.Platform
		available   []v1.Platform
		wantMatched []v1.Platform
		wantMissing []v1.Platform
	}{
		{
			name:        "all available",
			configured:  []v1.Platform{amd64, arm64},
			available:   []v1.Platform{amd64, arm64},
			wantMatched: []v1.Platform{amd64, arm64},
			wantMissing: nil,
		},
		{
			name:        "partial intersection",
			configured:  []v1.Platform{amd64, arm64},
			available:   []v1.Platform{arm64},
			wantMatched: []v1.Platform{arm64},
			wantMissing: []v1.Platform{amd64},
		},
		{
			name:        "empty intersection",
			configured:  []v1.Platform{amd64},
			available:   []v1.Platform{arm64},
			wantMatched: nil,
			wantMissing: []v1.Platform{amd64},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, missing := matchPlatforms(tt.configured, tt.available)
			if !platformsEqual(matched, tt.wantMatched) {
				t.Fatalf("matched: expected %v, got %v", tt.wantMatched, matched)
			}
			if !platformsEqual(missing, tt.wantMissing) {
				t.Fatalf("missing: expected %v, got %v", tt.wantMissing, missing)
			}
		})
	}
}

func TestNoMatchingPlatformError(t *testing.T) {
	err := noMatchingPlatformError([]v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	})
	want := "none of the configured platforms are available in the source image: linux/amd64, linux/arm64"
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}

func TestCheckPlatforms(t *testing.T) {
	var (
		amd64 = v1.Platform{OS: "linux", Architecture: "amd64"}
		arm64 = v1.Platform{OS: "linux", Architecture: "arm64"}
	)

	// matchPlatforms is covered by TestMatchPlatforms; here we only assert the
	// decision checkPlatforms owns: error when nothing matches, succeed otherwise.
	tests := []struct {
		name       string
		configured []v1.Platform
		available  []v1.Platform
		wantErr    bool
	}{
		{
			name:       "partial intersection succeeds",
			configured: []v1.Platform{amd64, arm64},
			available:  []v1.Platform{arm64},
			wantErr:    false,
		},
		{
			name:       "empty intersection fails",
			configured: []v1.Platform{amd64},
			available:  []v1.Platform{arm64},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkPlatforms(context.Background(), "registry/image:tag", tt.configured, tt.available)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func platformsEqual(a, b []v1.Platform) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].String() != b[i].String() {
			return false
		}
	}
	return true
}
