// Package tlstrust keeps the TLS settings of the registry clients in step with the `tls` key
// of the global config and the CA Secrets it names, in the cluster resource namespace.
package tlstrust

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
)

// Target receives the TLS settings of every registry host: a *registry.Client.
type Target interface {
	SetTLS(map[string]registry.TLSHost)
}

var errNotFound = errors.New("not found")

// Trust hands its targets the TLS settings of the `tls` key, each caSecretRef resolved to the
// bundle its Secret holds. It reads the Secrets from a cache of the cluster resource namespace
// and applies every change of the config or of a named Secret at once. A Secret it cannot use
// leaves its host unusable, never verified more weakly.
type Trust struct {
	secrets   cache.Cache
	namespace string
	targets   []Target
	log       logr.Logger
	// synced is set once the cache has synced, ready once the targets hold what it read.
	synced atomic.Bool
	ready  atomic.Bool

	mu  sync.Mutex
	tls config.TLS
	// state is what each host's CA was last logged as, the error or "" once loaded, keyed by
	// host and CA source, to log each change once.
	state map[stateKey]string
}

// New returns a Trust reading Secrets in namespace from secrets, a cache of that namespace,
// and hands the settings of cfg to targets at once. Until Start has read the Secrets, the
// requests to a host with a caSecretRef wait.
func New(secrets cache.Cache, namespace string, cfg *config.Config, targets ...Target) *Trust {
	t := &Trust{
		secrets:   secrets,
		namespace: namespace,
		targets:   targets,
		log:       logf.Log.WithName("registry-tls"),
		tls:       cfg.TLS,
		state:     map[stateKey]string{},
	}
	t.apply(context.Background())
	return t
}

// SetConfig applies a reloaded config.
func (t *Trust) SetConfig(cfg *config.Config) {
	t.mu.Lock()
	t.tls = cfg.TLS
	t.mu.Unlock()
	t.apply(context.Background())
}

// Start reads the Secrets once the cache has synced, then on every event of a Secret a
// caSecretRef names, until ctx is done.
func (t *Trust) Start(ctx context.Context) error {
	informer, err := t.secrets.GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return err
	}
	onEvent := func(obj any) {
		if t.synced.Load() && t.names(obj) {
			t.apply(ctx)
		}
	}
	registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    onEvent,
		UpdateFunc: func(_, obj any) { onEvent(obj) },
		DeleteFunc: onEvent,
	})
	if err != nil {
		return err
	}
	defer func() { _ = informer.RemoveEventHandler(registration) }()
	// A cache that never syncs ends with ctx: a shutdown, not a failure.
	if !t.secrets.WaitForCacheSync(ctx) {
		return nil
	}
	t.synced.Store(true)
	t.apply(ctx)
	t.ready.Store(true)
	<-ctx.Done()
	return nil
}

// NeedLeaderElection is false: a replica waiting for the lease must hold the settings when
// it takes over, and every webhook replica sends requests.
func (t *Trust) NeedLeaderElection() bool { return false }

// Ready is a readyz check: it fails until the CA Secrets have been read once.
func (t *Trust) Ready(*http.Request) error {
	if !t.ready.Load() {
		return errors.New("the CA Secrets of the tls key are not read yet")
	}
	return nil
}

// names reports whether obj is a Secret some caSecretRef names.
func (t *Trust) names(obj any) bool {
	if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	secret, ok := obj.(client.Object)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, entry := range t.tls {
		if entry.CASecretRef != nil && entry.CASecretRef.Name == secret.GetName() {
			return true
		}
	}
	return false
}

// apply resolves every entry and hands the result to the targets. An entry that sets neither
// caSecretRef nor insecureSkipVerify is no entry: its host is verified against the system
// trust store.
func (t *Trust) apply(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hosts := map[string]registry.TLSHost{}
	for host, entry := range t.tls {
		switch {
		case entry.InsecureSkipVerify:
			hosts[host] = registry.TLSHost{InsecureSkipVerify: true}
		case entry.CASecretRef != nil:
			hosts[host] = t.resolve(ctx, host, *entry.CASecretRef)
		}
	}
	for key := range t.state {
		if hosts[key.host].CASource != key.source {
			delete(t.state, key)
		}
	}
	for _, target := range t.targets {
		target.SetTLS(hosts)
	}
}

// stateKey is one CA of one host, as logged.
type stateKey struct{ host, source string }

// resolve reads the bundle of ref, and logs each change of the host's state.
func (t *Trust) resolve(ctx context.Context, host string, ref config.CASecretRef) registry.TLSHost {
	settings := registry.TLSHost{CASource: fmt.Sprintf("CA Secret %s/%s, key %s", t.namespace, ref.Name, ref.Key)}
	if !t.synced.Load() {
		// Not read yet: the requests to the host wait for the first read rather than fail.
		return registry.PendingTLSHost(settings.CASource)
	}
	var secret corev1.Secret
	err := t.secrets.Get(ctx, client.ObjectKey{Namespace: t.namespace, Name: ref.Name}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		settings.CAError = errNotFound
	case err != nil:
		settings.CAError = err
	default:
		bundle, ok := secret.Data[ref.Key]
		if !ok {
			settings.CAError = fmt.Errorf("the Secret has no key %s", ref.Key)
			break
		}
		// Checked here too, so that an unusable bundle is reported and logged as one.
		if _, err := registry.ParseCABundle(bundle); err != nil {
			settings.CAError = err
			break
		}
		settings.CABundle = bundle
	}

	state := ""
	if settings.CAError != nil {
		state = settings.CAError.Error()
	}
	key := stateKey{host: host, source: settings.CASource}
	if previous, seen := t.state[key]; seen && previous == state {
		return settings
	}
	t.state[key] = state
	log := t.log.WithValues("registry", host, "secret", klog.KRef(t.namespace, ref.Name))
	if settings.CAError != nil {
		log.Info("Failed to load registry CA bundle", "error", state)
	} else {
		log.Info("Loaded registry CA bundle")
	}
	return settings
}
