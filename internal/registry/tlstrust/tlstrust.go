// Package tlstrust keeps the TLS settings of the registry clients in step with the `tls` key
// of the global config and the CA Secrets it names, in the cluster resource namespace.
package tlstrust

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// Target receives the TLS settings of every registry host: a *registry.Client.
type Target interface {
	SetTLS(map[string]registry.TLSHost)
}

// Trust is a stub.
type Trust struct{}

// New is a stub.
func New(cache.Cache, string, *config.Config, ...Target) *Trust { return &Trust{} }

// SetConfig is a stub.
func (t *Trust) SetConfig(*config.Config) {}

// Start is a stub.
func (t *Trust) Start(context.Context) error { return nil }

// NeedLeaderElection is a stub.
func (t *Trust) NeedLeaderElection() bool { return false }

// Ready is a stub.
func (t *Trust) Ready(*http.Request) error { return nil }
