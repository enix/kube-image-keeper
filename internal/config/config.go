package config

import (
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Routing              Routing              `koanf:"routing"`
	RegistriesMonitoring RegistriesMonitoring `koanf:"registriesMonitoring"`
	Metrics              Metrics              `koanf:"metrics"`
}

type Metrics struct {
	ImageLastMonitorAgeMinutes HistogramConfig `koanf:"imageLastMonitorAgeMinutes"`
}

type Routing struct {
	ActiveCheck ActiveCheck `koanf:"activeCheck"`
}

type ActiveCheck struct {
	Timeout time.Duration `koanf:"timeout"`
}

type RegistriesMonitoring struct {
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

var defaultConfig = Config{
	Routing: Routing{
		ActiveCheck: ActiveCheck{
			Timeout: time.Second,
		},
	},
	RegistriesMonitoring: RegistriesMonitoring{
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

func Load(path string) (*Config, error) {
	return load(file.Provider(path), yaml.Parser())
}

func load(provider koanf.Provider, parser koanf.Parser) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(defaultConfig, "koanf"), nil); err != nil {
		return nil, err
	}

	if err := k.Load(provider, parser); err != nil {
		return nil, err
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
