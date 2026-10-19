package registry

import (
	"context"

	"github.com/enix/kube-image-keeper/internal/config"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LoadTLSSettings builds the TLS settings of the registry clients from the tls
// configuration, reading its CA Secrets. Stub.
func LoadTLSSettings(ctx context.Context, reader client.Reader, cfg *config.TLS) *TLSSettings {
	return nil
}
