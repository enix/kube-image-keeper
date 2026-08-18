package kuik

import (
	"testing"
	"time"

	"github.com/enix/kube-image-keeper/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// The per-registry resolveDigest override is a three-state boolean: an items
// entry leaving it unset (nil) inherits the default, an explicit false opts a
// single registry out of an enabled default, an explicit true opts it in.
func TestRegistryConfigResolveDigest(t *testing.T) {
	tests := []struct {
		name        string
		defaultFlag *bool
		override    *config.RegistryMonitoring
		want        bool
	}{
		{
			name: "no override, unset default is disabled",
		},
		{
			name:        "no override inherits enabled default",
			defaultFlag: boolPtr(true),
			want:        true,
		},
		{
			name:        "unset override inherits enabled default",
			defaultFlag: boolPtr(true),
			override:    &config.RegistryMonitoring{},
			want:        true,
		},
		{
			name:        "explicit false overrides enabled default",
			defaultFlag: boolPtr(true),
			override:    &config.RegistryMonitoring{ResolveDigest: boolPtr(false)},
			want:        false,
		},
		{
			name:     "explicit true overrides unset default",
			override: &config.RegistryMonitoring{ResolveDigest: boolPtr(true)},
			want:     true,
		},
		{
			name:        "explicit true overrides disabled default",
			defaultFlag: boolPtr(false),
			override:    &config.RegistryMonitoring{ResolveDigest: boolPtr(true)},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registries := config.Registries{
				Default: config.RegistryMonitoring{
					Method:         "HEAD",
					Interval:       3 * time.Hour,
					MaxPerInterval: 25,
					ResolveDigest:  tt.defaultFlag,
				},
				Items: map[string]config.RegistryMonitoring{},
			}
			if tt.override != nil {
				registries.Items["registry.example.com"] = *tt.override
			}

			r := &ClusterImageSetAvailabilityReconciler{
				Config: &config.Config{
					Monitoring: config.Monitoring{Registries: registries},
				},
			}

			merged := r.registryConfig("registry.example.com")
			if got := merged.ResolveDigestEnabled(); got != tt.want {
				t.Errorf("ResolveDigestEnabled() = %v, want %v (default=%v, override=%+v)", got, tt.want, tt.defaultFlag, tt.override)
			}

			// The merge must not clobber unrelated defaults when the override
			// only sets resolveDigest.
			if merged.Method != "HEAD" || merged.MaxPerInterval != 25 {
				t.Errorf("merge clobbered unrelated defaults: %+v", merged)
			}
		})
	}
}
