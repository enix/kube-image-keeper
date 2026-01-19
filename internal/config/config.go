package config

import (
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Routing Routing `koanf:"routing"`
}

type Routing struct {
	ActiveCheck ActiveCheck `koanf:"activeCheck"`
}

type ActiveCheck struct {
	Timeout time.Duration `koanf:"timeout"`
}

var defaultConfig = Config{
	Routing: Routing{
		ActiveCheck: ActiveCheck{
			Timeout: time.Second,
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
