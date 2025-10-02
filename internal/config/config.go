package config

import (
	"log"
	"reflect"
	"regexp"

	"github.com/enix/kube-image-keeper/internal/registry/routing"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Monitoring struct {
	Enabled bool `koanf:"enabled"`
}

type Config struct {
	Monitoring Monitoring      `koanf:"monitoring"`
	Routing    routing.Routing `koanf:"routing"`
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

	k.UnmarshalWithConf("", config, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(stringToRegexp),
		},
	})

	return config, nil
}

func stringToRegexp(from, to reflect.Type, data any) (any, error) {
	if from.Kind() == reflect.String && to == reflect.TypeOf(&regexp.Regexp{}) {
		re, err := regexp.Compile(data.(string))
		if err != nil {
			return nil, err
		}
		return re, nil
	}
	return data, nil
}
