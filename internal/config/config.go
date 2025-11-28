package config

import (
	"log"
	"regexp"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"k8s.io/apimachinery/pkg/types"
)

type Config struct {
	Monitoring  Monitoring  `koanf:"monitoring"`
	Mirroring   Mirroring   `koanf:"mirroring"`
	ActiveCheck ActiveCheck `koanf:"activeCheck"`
}

type Monitoring struct {
	Enabled bool `koanf:"enabled"`
}

type Mirroring struct {
	Secrets map[string]types.NamespacedName `koanf:"secrets"`
}

type ActiveCheck struct {
	Timeout time.Duration `koanf:"timeout"`
}

type Strategy struct {
	Paths      []*regexp.Regexp `koanf:"paths"`
	Registries []Registry       `koanf:"registries"`
}

type Registry struct {
	Url              string `koanf:"url"`
	MirroringEnabled bool   `koanf:"mirroringEnabled"`
}

func Load(path string) (*Config, error) {
	return load(file.Provider(path), yaml.Parser())
}

func load(provider koanf.Provider, parser koanf.Parser) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(provider, parser); err != nil {
		log.Fatalf("error loading config: %v", err)
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
