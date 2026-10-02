// Package secretsyncer materialises the pull Secrets the Pod webhook injects: one Secret per
// pair (routing resource, namespace), applied blind and kept valid. See
// docs/v3/walkthroughs/03-secret-syncer-reconciliation.md.
package secretsyncer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultDebounce is how long the events of one pair are collapsed into one reconcile: a
// rollout is a burst of pod creates and deletes that almost always leaves the pair unchanged.
const DefaultDebounce = time.Second

// EventPullSecretInjectionFailed is emitted on a resource whose credential could not be
// resolved for a pair.
const EventPullSecretInjectionFailed = "PullSecretInjectionFailed"

// Options are what a Syncer reads and reports with.
type Options struct {
	// Namespace is the install namespace, where a secretRef resolves.
	Namespace string
	// Recorder emits the events on the resources.
	Recorder events.EventRecorder
	// Registerer exports kuik_secret_applies_total.
	Registerer prometheus.Registerer
	// Debounce delays the reconcile of a pair. Zero means DefaultDebounce.
	Debounce time.Duration
	// Now reads the time the grace period is counted with. Nil means time.Now.
	Now func() time.Time
}

// Syncer applies the pull Secret of every pair.
type Syncer struct{}

// New returns a syncer writing with c, which must read from a cache built with CacheOptions.
func New(c client.Client, opts Options) (*Syncer, error) {
	return &Syncer{}, nil
}

// SetupWithManager registers the syncer and its watches.
func (s *Syncer) SetupWithManager(mgr ctrl.Manager) error {
	return nil
}

// CacheOptions are the cache options of the syncer's manager: Secrets are watched in the
// install namespace only, the one namespace the syncer may read them in.
func CacheOptions(namespace string) cache.Options {
	return cache.Options{}
}
