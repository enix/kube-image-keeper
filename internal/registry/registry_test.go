package registry

import (
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestMissingPlatformError(t *testing.T) {
	tests := []struct {
		name     string
		platform v1.Platform
		want     string
	}{
		{
			name:     "architecture only",
			platform: v1.Platform{Architecture: "amd64"},
			want:     "missing platform: amd64",
		},
		{
			name:     "os and architecture",
			platform: v1.Platform{OS: "linux", Architecture: "amd64"},
			want:     "missing platform: linux/amd64",
		},
		{
			name:     "os, architecture and variant",
			platform: v1.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			want:     "missing platform: linux/arm64/v8",
		},
		{
			name:     "architecture and variant without os",
			platform: v1.Platform{Architecture: "arm", Variant: "v7"},
			want:     "missing platform: arm/v7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := missingPlatformError(tt.platform)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if err.Error() != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, err.Error())
			}
		})
	}
}
