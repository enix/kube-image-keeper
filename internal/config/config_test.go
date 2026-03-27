package config

import (
	"strings"
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name:   "default config is valid",
			mutate: func(c *Config) {},
		},
		{
			name: "multiple valid platforms",
			mutate: func(c *Config) {
				c.Mirroring.Platforms = []Platform{
					{OS: "linux", Architecture: "amd64"},
					{OS: "linux", Architecture: "arm64", Variant: "v8"},
				}
			},
		},
		{
			name: "empty platforms list is rejected",
			mutate: func(c *Config) {
				c.Mirroring.Platforms = nil
			},
			wantError: "Platforms",
		},
		{
			name: "platform with empty architecture is rejected",
			mutate: func(c *Config) {
				c.Mirroring.Platforms = []Platform{
					{OS: "linux"},
				}
			},
			wantError: "Architecture",
		},
		{
			name: "second platform with empty architecture is rejected",
			mutate: func(c *Config) {
				c.Mirroring.Platforms = []Platform{
					{Architecture: "amd64"},
					{OS: "linux", Variant: "v8"},
				}
			},
			wantError: "Architecture",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadDefault()
			if err != nil {
				t.Fatalf("LoadDefault: %v", err)
			}
			tt.mutate(cfg)
			err = cfg.Validate()
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}
