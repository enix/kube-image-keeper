package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
		// alsoWantError is a second part the error must contain, when it joins
		// several errors
		alsoWantError string
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
		{
			name: "tls with a private CA and a self-signed host is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{
					"harbor.corp.tld":       {CASecretRef: caRef("kuik-system", "harbor-ca")},
					"registry.lab.tld:5000": {InsecureSkipVerify: true},
				}
			},
		},
		{
			name: "tls host key with a port is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{"10.0.0.1:5000": {InsecureSkipVerify: true}}
			},
		},
		{
			name: "tls host key localhost with a port is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{"localhost:5000": {InsecureSkipVerify: true}}
			},
		},
		{
			name: "tls host key with uppercase letters is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{"Harbor.corp.tld": {InsecureSkipVerify: true}}
			},
		},
		{
			name: "tls index.docker.io host key is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{"index.docker.io": {CASecretRef: caRef("kuik-system", "proxy-ca")}}
			},
		},
		{
			name: "tls empty host entry is valid",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld": {}}
			},
		},
		{
			name: "tls host entry with both caSecretRef and insecureSkipVerify is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld": {CASecretRef: caRef("kuik-system", "harbor-ca"), InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld]: caSecretRef and insecureSkipVerify are exclusive",
		},
		{
			name: "tls caSecretRef with an empty name is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld": {CASecretRef: caRef("kuik-system", "")}}
			},
			wantError: "tls[harbor.corp.tld].caSecretRef.name",
		},
		{
			name: "tls caSecretRef with an empty namespace is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld": {CASecretRef: caRef("", "harbor-ca")}}
			},
			wantError: "tls[harbor.corp.tld].caSecretRef.namespace",
		},
		{
			name: "tls two host keys naming the same host are rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"Harbor.corp.tld": {InsecureSkipVerify: true}, "harbor.corp.tld": {InsecureSkipVerify: true}}
			},
			wantError: "harbor.corp.tld is declared twice",
		},
		{
			name: "tls index.docker.io and docker.io host keys are rejected together",
			mutate: func(c *Config) {
				c.TLS = TLS{"docker.io": {InsecureSkipVerify: true}, "index.docker.io": {InsecureSkipVerify: true}}
			},
			wantError: "docker.io is declared twice",
		},
		{
			name: "tls host key with a scheme is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"https://harbor.corp.tld": {InsecureSkipVerify: true}}
			},
			wantError: "tls[https://harbor.corp.tld]",
		},
		{
			name: "tls host key with a path is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld/library": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld/library]",
		},
		{
			name: "tls host key with a non-numeric port is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld:https": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld:https]",
		},
		{
			name: "tls empty host key is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"": {InsecureSkipVerify: true}}
			},
			wantError: "tls[]",
		},
		{
			name: "tls host key with two ports is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld:5000:6000": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld:5000:6000]",
		},
		{
			name: "tls host key with an unbracketed IPv6 address is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"fe80::1": {InsecureSkipVerify: true}}
			},
			wantError: "tls[fe80::1]",
		},
		{
			name: "tls host key with an underscore is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor_corp.tld": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor_corp.tld]",
		},
		{
			name: "tls host key with a single label and no port is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor]",
		},
		{
			name: "tls host key with an empty port is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld:": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld:]",
		},
		{
			name: "tls host key with port 0 is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld:0": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld:0]",
		},
		{
			name: "tls host key with port 65536 is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld:65536": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld:65536]",
		},
		{
			name: "tls host key with userinfo is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"user@harbor.corp.tld": {InsecureSkipVerify: true}}
			},
			wantError: "tls[user@harbor.corp.tld]",
		},
		{
			name: "tls host key with a query is rejected",
			mutate: func(c *Config) {
				c.TLS = TLS{"harbor.corp.tld?x": {InsecureSkipVerify: true}}
			},
			wantError: "tls[harbor.corp.tld?x]",
		},
		{
			name: "tls errors are reported along with the other validation errors",
			mutate: func(c *Config) {
				c.Mirroring.Platforms = nil
				c.TLS = TLS{"harbor.corp.tld": {CASecretRef: caRef("", "harbor-ca")}}
			},
			wantError:     "Platforms",
			alsoWantError: "tls[harbor.corp.tld]",
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
			if !strings.Contains(err.Error(), tt.alsoWantError) {
				t.Fatalf("expected error containing %q, got %q", tt.alsoWantError, err.Error())
			}
		})
	}
}

func TestLoad_TLS(t *testing.T) {
	t.Run("default config has no tls entry", func(t *testing.T) {
		cfg, err := LoadDefault()
		if err != nil {
			t.Fatalf("LoadDefault: %v", err)
		}
		if len(cfg.TLS) != 0 {
			t.Fatalf("expected an empty tls config, got %+v", cfg.TLS)
		}
	})

	cfg := loadYAML(t, `
tls:
  harbor.corp.tld:
    caSecretRef:
      namespace: kuik-system
      name: harbor-ca
  registry.lab.tld:5000:
    insecureSkipVerify: true
  quay.corp.tld:
    caSecretRef:
      namespace: kuik-system
      name: quay-ca
      key: bundle.pem
  index.docker.io:
    insecureSkipVerify: true
  Ghcr.Corp.tld:
    insecureSkipVerify: true
`)

	t.Run("caSecretRef key defaults to ca.crt", func(t *testing.T) {
		want := CASecretRef{Namespace: "kuik-system", Name: "harbor-ca", Key: "ca.crt"}
		if got := cfg.TLS.Hosts()["harbor.corp.tld"].CASecretRef; got == nil || *got != want {
			t.Fatalf("expected %+v, got %+v", want, got)
		}
	})

	t.Run("explicit caSecretRef key is kept", func(t *testing.T) {
		want := CASecretRef{Namespace: "kuik-system", Name: "quay-ca", Key: "bundle.pem"}
		if got := cfg.TLS.Hosts()["quay.corp.tld"].CASecretRef; got == nil || *got != want {
			t.Fatalf("expected %+v, got %+v", want, got)
		}
	})

	t.Run("dotted host keys with a port are kept whole", func(t *testing.T) {
		if entry, ok := cfg.TLS.Hosts()["registry.lab.tld:5000"]; !ok || !entry.InsecureSkipVerify {
			t.Fatalf("expected an insecureSkipVerify entry for registry.lab.tld:5000, got %+v", cfg.TLS.Hosts())
		}
	})

	t.Run("an index.docker.io host key is stored as docker.io", func(t *testing.T) {
		hosts := cfg.TLS.Hosts()
		if entry, ok := hosts["docker.io"]; !ok || !entry.InsecureSkipVerify {
			t.Fatalf("expected a docker.io entry, got %+v", hosts)
		}
		if _, ok := hosts["index.docker.io"]; ok {
			t.Fatalf("expected no index.docker.io entry, got %+v", hosts)
		}
	})

	t.Run("a host key is stored lowercased", func(t *testing.T) {
		hosts := cfg.TLS.Hosts()
		if entry, ok := hosts["ghcr.corp.tld"]; !ok || !entry.InsecureSkipVerify {
			t.Fatalf("expected a ghcr.corp.tld entry, got %+v", hosts)
		}
		if len(hosts) != 5 {
			t.Fatalf("expected 5 hosts, got %+v", hosts)
		}
	})
}

func loadYAML(t *testing.T, content string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func caRef(namespace, name string) *CASecretRef {
	return &CASecretRef{Namespace: namespace, Name: name, Key: "ca.crt"}
}
