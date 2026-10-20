package main

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/tlstrust"
)

// The CA Secrets are read in the cluster resource namespace, by the two processes that send
// registry requests.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=webhook
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch,namespace=kuik-system,roleName=reconciler

// newSecretCache is the cache of the Secrets of the cluster resource namespace alone, started
// with mgr on every replica: one per process, shared by every reader of those Secrets. A
// replica waiting for the lease reads the CA Secrets too, or it would never report ready.
func newSecretCache(mgr ctrl.Manager, namespace string) (cache.Cache, error) {
	secrets, err := cache.New(mgr.GetConfig(), cache.Options{
		Scheme:            mgr.GetScheme(),
		Mapper:            mgr.GetRESTMapper(),
		DefaultNamespaces: map[string]cache.Config{namespace: {}},
	})
	if err != nil {
		return nil, err
	}
	if err := mgr.Add(everyReplica{secrets}); err != nil {
		return nil, err
	}
	return secrets, nil
}

// everyReplica runs a runnable on every replica. controller-runtime starts a runnable that
// does not say otherwise, a cache among them, only once the replica holds the lease.
type everyReplica struct{ manager.Runnable }

// NeedLeaderElection is false: the runnable runs with or without the lease.
func (everyReplica) NeedLeaderElection() bool { return false }

// setupRegistryTLS hands the TLS settings of the global config to clients, reading the CA
// Secrets from secrets, and holds the process not ready until those Secrets are read: an
// origin probed before would wait for them. It returns the Trust that config reloads go to.
func setupRegistryTLS(mgr ctrl.Manager, secrets cache.Cache, namespace string, cfg *config.Config,
	clients ...*registry.Client) (*tlstrust.Trust, error) {
	targets := make([]tlstrust.Target, 0, len(clients))
	for _, c := range clients {
		targets = append(targets, c)
	}
	trust := tlstrust.New(secrets, namespace, cfg, targets...)
	if err := mgr.Add(trust); err != nil {
		return nil, err
	}
	if err := mgr.AddReadyzCheck("registry-tls", trust.Ready); err != nil {
		return nil, err
	}
	return trust, nil
}
