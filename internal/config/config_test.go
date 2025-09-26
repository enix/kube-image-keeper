package config

import (
	"regexp"
	"strings"
	"testing"

	"github.com/enix/kube-image-keeper/internal/registry/routing"
	_ "github.com/enix/kube-image-keeper/internal/testsetup"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	. "github.com/onsi/gomega"
)

func Test_load(t *testing.T) {
	g := NewWithT(t)

	rawYaml := []byte(strings.ReplaceAll(`
routing:
	strategies:
		- paths:
				- enix/x509-certificate-exporter
				- nginx
				- ^bitnami/.+$
			registries:
				- docker.io
				- ghcr.io
		- paths:
				- enix/.+
			registries:
				- quay.io
				- private.example.com
`, "\t", "  "))

	expectedConfig := Config{
		Routing: routing.Routing{
			Strategies: []routing.Strategy{
				{
					Paths: []*regexp.Regexp{
						regexp.MustCompile("enix/x509-certificate-exporter"),
						regexp.MustCompile("nginx"),
						regexp.MustCompile("^bitnami/.+$"),
					},
					Registries: []string{"docker.io", "ghcr.io"},
				},
				{
					Paths: []*regexp.Regexp{
						regexp.MustCompile("enix/.+"),
					},
					Registries: []string{"quay.io", "private.example.com"},
				},
			},
		},
	}

	config, err := load(rawbytes.Provider(rawYaml), yaml.Parser())

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(*config).To(Equal(expectedConfig))
}
