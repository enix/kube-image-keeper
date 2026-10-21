package registry

import (
	"context"
	"fmt"

	"github.com/enix/kube-image-keeper/internal/config"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LoadTLSSettings builds the TLS settings of the registry clients from the tls
// configuration, reading its CA Secrets through reader. Called each time a
// client is built, so that a change of a CA Secret applies without a restart.
// A CA Secret that cannot be read does not fail the settings: the bundle carries
// the error, and every request to its host fails with it.
func LoadTLSSettings(ctx context.Context, reader client.Reader, cfg *config.TLS) *TLSSettings {
	settings := &TLSSettings{}
	if cfg == nil || len(*cfg) == 0 {
		return settings
	}
	hosts := cfg.Hosts()
	settings.Hosts = make(map[string]TLSEntry, len(hosts))
	for host, entry := range hosts {
		loaded := TLSEntry{InsecureSkipVerify: entry.InsecureSkipVerify}
		if entry.CASecretRef != nil {
			bundle := loadCABundle(ctx, reader, entry.CASecretRef)
			loaded.CA = &bundle
		}
		settings.Hosts[host] = loaded
	}
	return settings
}

func loadCABundle(ctx context.Context, reader client.Reader, ref *config.CASecretRef) CABundle {
	key := ref.Key
	if key == "" {
		key = config.DefaultCASecretKey
	}
	bundle := CABundle{Source: fmt.Sprintf("Secret %s/%s, key %s", ref.Namespace, ref.Name, key)}

	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		bundle.Err = fmt.Errorf("could not read the Secret: %w", err)
		return bundle
	}
	data, ok := secret.Data[key]
	if !ok {
		bundle.Err = fmt.Errorf("the Secret has no key %q", key)
		return bundle
	}
	bundle.PEM = data
	return bundle
}
