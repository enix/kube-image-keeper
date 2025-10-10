package config

import (
	"regexp"
	"strings"
	"testing"

	_ "github.com/enix/kube-image-keeper/internal/testsetup"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	. "github.com/onsi/gomega"
)

func Test_load(t *testing.T) {
	g := NewWithT(t)

	rawYaml := []byte(strings.ReplaceAll(`
strategies:
	- paths:
			- enix/x509-certificate-exporter
			- nginx
			- ^bitnami/.+$
		registries:
			- url: docker.io
			- url: ghcr.io
	- paths:
			- enix/.+
		registries:
			- url: quay.io
			- url: private.example.com
				mirroringEnabled: true
`, "\t", "  "))

	expectedConfig := Config{
		Strategies: []Strategy{
			{
				Paths: []*regexp.Regexp{
					regexp.MustCompile("enix/x509-certificate-exporter"),
					regexp.MustCompile("nginx"),
					regexp.MustCompile("^bitnami/.+$"),
				},
				Registries: []Registry{
					{Url: "docker.io"},
					{Url: "ghcr.io"},
				},
			},
			{
				Paths: []*regexp.Regexp{
					regexp.MustCompile("enix/.+"),
				},
				Registries: []Registry{
					{Url: "quay.io"},
					{Url: "private.example.com", MirroringEnabled: true},
				},
			},
		},
	}

	config, err := load(rawbytes.Provider(rawYaml), yaml.Parser())

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(*config).To(Equal(expectedConfig))
}
