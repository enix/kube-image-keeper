package config

import (
	"errors"
	"net/http"
	"os"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Routing    Routing    `koanf:"routing"`
	Mirroring  Mirroring  `koanf:"mirroring"`
	Monitoring Monitoring `koanf:"monitoring"`
	Metrics    Metrics    `koanf:"metrics"`
}

type Routing struct {
	ActiveCheck                            ActiveCheck `koanf:"activeCheck"`
	RewriteOnNeverImagePullPolicy          bool        `koanf:"rewriteOnNeverImagePullPolicy"`
	HonorPrioritiesOnAlwaysImagePullPolicy bool        `koanf:"honorPrioritiesOnAlwaysImagePullPolicy"`
}

type ActiveCheck struct {
	Timeout            time.Duration      `koanf:"timeout"`
	StaleMirrorCleanup StaleMirrorCleanup `koanf:"staleMirrorCleanup"`
}

type StaleMirrorCleanup struct {
	MaxConcurrent int           `koanf:"maxConcurrent"`
	Timeout       time.Duration `koanf:"timeout"`
}

type Mirroring struct {
	Platforms []Platform `koanf:"platforms" validate:"min=1,dive"`

	// Registries maps an upstream registry hostname (e.g. "index.docker.io",
	// "quay.io") to per-registry mirroring configuration. The hostname key
	// must match what go-containerregistry's name.ParseReference returns from
	// .Context().RegistryStr(), i.e. "index.docker.io" — NOT "docker.io".
	Registries map[string]MirrorRegistry `koanf:"registries"`
}

// MirrorRegistry holds per-upstream-registry mirror controller config.
//
// FallbackCredentialSecret is the Secret the mirror controller uses to
// authenticate to the upstream when no pod in the cluster contributes
// credentials via pod.Spec.ImagePullSecrets. This is the common case once
// the kuik webhook has rewritten workloads to pull from the mirror — the
// only pods carrying the original `docker.io/...` image string then
// disappear, leaving the mirror controller with no pod-derived secrets.
// Mirrors the shape of Monitoring.Registries.<host>.fallbackCredentialSecret
// used by the ClusterImageSetAvailability controller.
type MirrorRegistry struct {
	FallbackCredentialSecret *kuikv1alpha1.CredentialSecret `koanf:"fallbackCredentialSecret"`
}

type Platform struct {
	OS           string `koanf:"os"`
	Architecture string `koanf:"architecture" validate:"required"`
	Variant      string `koanf:"variant"`
}

type Monitoring struct {
	Registries Registries `koanf:"registries"`
}

type Registries struct {
	Default RegistryMonitoring            `koanf:"default"`
	Items   map[string]RegistryMonitoring `koanf:"items"`
}

type RegistryMonitoring struct {
	Method                   string                         `koanf:"method"`
	Interval                 time.Duration                  `koanf:"interval"`
	MaxPerInterval           int                            `koanf:"maxPerInterval"`
	Timeout                  time.Duration                  `koanf:"timeout"`
	FallbackCredentialSecret *kuikv1alpha1.CredentialSecret `koanf:"fallbackCredentialSecret"`
}

type Metrics struct {
	ImageLastMonitorAgeMinutes HistogramConfig `koanf:"imageLastMonitorAgeMinutes"`
}

var defaultConfig = Config{
	Routing: Routing{
		ActiveCheck: ActiveCheck{
			Timeout: time.Second,
			StaleMirrorCleanup: StaleMirrorCleanup{
				MaxConcurrent: 10,
				Timeout:       5 * time.Second,
			},
		},
		RewriteOnNeverImagePullPolicy:          false,
		HonorPrioritiesOnAlwaysImagePullPolicy: false,
	},
	Mirroring: Mirroring{
		Platforms: []Platform{
			{Architecture: "amd64"},
		},
	},
	Monitoring: Monitoring{
		Registries: Registries{
			Default: RegistryMonitoring{
				Method:         http.MethodHead,
				Interval:       3 * time.Hour,
				MaxPerInterval: 25,
			},
			Items: map[string]RegistryMonitoring{
				"docker.io": {
					Interval:       time.Hour,
					MaxPerInterval: 6,
				},
			},
		},
	},
	Metrics: Metrics{
		ImageLastMonitorAgeMinutes: HistogramConfig{
			BucketFactor:    1.1,
			ZeroThreshold:   1.0,
			MaxBucketNumber: 20,
			Legacy: LegacyHistogramConfig{
				Start:  1,
				Factor: 1.94,
				Count:  12,
				Min:    1,
				Max:    24 * 60,
				Custom: []float64{1, 5, 10, 30, 60, 120, 180, 360, 720, 1440},
			},
		},
	},
}

func (c *Config) Validate() error {
	return validator.New().Struct(c)
}

func LoadDefault() (*Config, error) {
	return load(nil, nil)
}

func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LoadDefault()
		}
		return nil, err
	}
	return load(file.Provider(path), yaml.Parser())
}

func load(provider koanf.Provider, parser koanf.Parser) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(defaultConfig, "koanf"), nil); err != nil {
		return nil, err
	}

	if provider != nil {
		if err := k.Load(provider, parser); err != nil {
			return nil, err
		}
	}

	config := &Config{}

	return config, k.UnmarshalWithConf("", config, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
			),
		},
	})
}
