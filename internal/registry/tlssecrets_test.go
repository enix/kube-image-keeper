package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/enix/kube-image-keeper/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func caSecret(name, key string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kuik-system", Name: name},
		Data:       map[string][]byte{key: data},
	}
}

func caSecretRef(key string) *config.CASecretRef {
	return &config.CASecretRef{Namespace: "kuik-system", Name: "harbor-ca", Key: key}
}

// caOf returns the CA bundle of harbor.corp.tld.
func caOf(t *testing.T, s *TLSSettings) CABundle {
	t.Helper()
	const host = "harbor.corp.tld"
	entry, ok := s.Hosts[host]
	if !ok || entry.CA == nil {
		t.Fatalf("expected a CA bundle for %s, got %+v", host, s.Hosts)
	}
	return *entry.CA
}

func loadSettings(t *testing.T, reader client.Reader, cfg config.TLS) *TLSSettings {
	t.Helper()
	s := LoadTLSSettings(context.Background(), reader, &cfg)
	if s == nil {
		t.Fatal("expected TLS settings, got nil")
	}
	return s
}

func TestLoadTLSSettings(t *testing.T) {
	t.Run("the CA of a caSecretRef is read from the key of its Secret", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithObjects(caSecret("harbor-ca", "bundle.pem", []byte("harbor PEM"))).Build()
		s := loadSettings(t, reader, config.TLS{"harbor.corp.tld": {CASecretRef: caSecretRef("bundle.pem")}})

		ca := caOf(t, s)
		if ca.Err != nil || string(ca.PEM) != "harbor PEM" {
			t.Errorf("CA = %+v, want the harbor PEM", ca)
		}
		if want := "Secret kuik-system/harbor-ca, key bundle.pem"; ca.Source != want {
			t.Errorf("CA source = %q, want %q", ca.Source, want)
		}
	})

	t.Run("a missing CA Secret gives a CA error naming the Secret", func(t *testing.T) {
		s := loadSettings(t, fake.NewClientBuilder().Build(), config.TLS{"harbor.corp.tld": {CASecretRef: caSecretRef("ca.crt")}})

		ca := caOf(t, s)
		if ca.Err == nil || !strings.Contains(ca.Err.Error(), "not found") {
			t.Errorf("expected a not found error, got %v", ca.Err)
		}
		if !strings.Contains(ca.Source, "Secret kuik-system/harbor-ca") {
			t.Errorf("expected the source to name the Secret, got %q", ca.Source)
		}
	})

	t.Run("a CA Secret without the key gives a CA error naming the Secret and the key", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithObjects(caSecret("harbor-ca", "tls.crt", []byte("PEM"))).Build()
		s := loadSettings(t, reader, config.TLS{"harbor.corp.tld": {CASecretRef: caSecretRef("ca.crt")}})

		ca := caOf(t, s)
		if ca.Err == nil || !strings.Contains(ca.Err.Error(), `no key "ca.crt"`) {
			t.Errorf("expected a missing key error, got %v", ca.Err)
		}
		if want := "Secret kuik-system/harbor-ca, key ca.crt"; ca.Source != want {
			t.Errorf("source = %q, want %q", ca.Source, want)
		}
	})

	t.Run("an insecureSkipVerify host entry is kept", func(t *testing.T) {
		s := loadSettings(t, fake.NewClientBuilder().Build(), config.TLS{"registry.lab.tld:5000": {InsecureSkipVerify: true}})

		entry, ok := s.Hosts["registry.lab.tld:5000"]
		if !ok || !entry.InsecureSkipVerify || entry.CA != nil {
			t.Errorf("expected an insecureSkipVerify entry without CA, got %+v (present: %v)", entry, ok)
		}
	})

	t.Run("host keys are named as the requests name them", func(t *testing.T) {
		s := loadSettings(t, fake.NewClientBuilder().Build(), config.TLS{
			"index.docker.io": {InsecureSkipVerify: true},
			"Harbor.corp.tld": {InsecureSkipVerify: true},
		})

		for _, host := range []string{"docker.io", "harbor.corp.tld"} {
			if !s.Hosts[host].InsecureSkipVerify {
				t.Errorf("expected an entry for %s, got %+v", host, s.Hosts)
			}
		}
	})

	t.Run("a change of a CA Secret is read by the next call", func(t *testing.T) {
		secret := caSecret("harbor-ca", "ca.crt", []byte("old PEM"))
		reader := fake.NewClientBuilder().WithObjects(secret).Build()
		cfg := config.TLS{"harbor.corp.tld": {CASecretRef: caSecretRef("ca.crt")}}
		_ = loadSettings(t, reader, cfg)

		secret.Data["ca.crt"] = []byte("new PEM")
		if err := reader.Update(context.Background(), secret); err != nil {
			t.Fatalf("update Secret: %v", err)
		}

		if ca := caOf(t, loadSettings(t, reader, cfg)); string(ca.PEM) != "new PEM" {
			t.Errorf("expected the new PEM, got %q", ca.PEM)
		}
	})
}
