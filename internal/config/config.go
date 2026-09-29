// Package config loads, validates and reloads the global config file of docs/v3/spec.md,
// "Global config".
package config

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/enix/kube-image-keeper/internal/auth"
)

// errNotImplemented is returned by the stubs until the package is implemented.
var errNotImplemented = errors.New("not implemented")

// reloadErrors counts the reloads rejected because the file did not parse or validate.
var reloadErrors = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "kuik_config_reload_errors_total",
	Help: "Reloads of the global config file rejected because it did not parse or validate",
})

// Duration is a duration written with its unit (`1m`, `10s`), or `0`.
type Duration struct {
	time.Duration
}

// Config is the global config file.
type Config struct {
	ClusterID    string                   `json:"clusterID"`
	Metrics      Metrics                  `json:"metrics"`
	Mirror       Mirror                   `json:"mirror"`
	Webhook      Webhook                  `json:"webhook"`
	Registries   Registries               `json:"registries"`
	FallbackAuth []auth.FallbackAuthEntry `json:"fallbackAuth"`
}

// Metrics holds the optional metrics.
type Metrics struct {
	CopyDuration bool `json:"copyDuration"`
}

// Mirror paces the destination an ImageMirror owns.
type Mirror struct {
	DestinationScan DestinationScan `json:"destinationScan"`
}

// DestinationScan is how often each ImageMirror re-reads its destination.
type DestinationScan struct {
	Interval Duration `json:"interval"`
}

// Webhook configures the admission path.
type Webhook struct {
	DemoteMirrorWithPullPolicyAlways bool              `json:"demoteMirrorWithPullPolicyAlways"`
	AvailabilityCheck                AvailabilityCheck `json:"availabilityCheck"`
}

// AvailabilityCheck configures the webhook's active check.
type AvailabilityCheck struct {
	Timeout             Duration         `json:"timeout"`
	ActiveCheckCache    ActiveCheckCache `json:"activeCheckCache"`
	DemoteKnownFailures bool             `json:"demoteKnownFailures"`
}

// ActiveCheckCache is the per-replica cache of active checks.
type ActiveCheckCache struct {
	TTL Duration `json:"ttl"`
}

// Registries paces the reads from each registry host: default, and one block per host.
type Registries struct {
	Default RegistryPacing
	Hosts   map[string]RegistryPacing
}

// RegistryPacing is one block of `registries`. Every field is a pointer: a field left out
// inherits from `default`, and `copy.timeout: 0` means no bound.
type RegistryPacing struct {
	Check *Window `json:"check,omitempty"`
	Copy  *Window `json:"copy,omitempty"`
}

// Window is the pace of one loop against a host.
type Window struct {
	Interval *Duration `json:"interval,omitempty"`
	Timeout  *Duration `json:"timeout,omitempty"`
}

// Pacing is the resolved pace of a host, `default` merged field by field.
type Pacing struct {
	Check ResolvedWindow
	Copy  ResolvedWindow
}

// ResolvedWindow is a window with every field set. A zero copy Timeout means no bound.
type ResolvedWindow struct {
	Interval time.Duration
	Timeout  time.Duration
}

// For returns the pace of a host: its block merged over `default`, field by field.
func (r Registries) For(host string) Pacing {
	return Pacing{}
}

// Load reads, defaults and validates the config file. An unknown key, at any level, rejects
// it.
func Load(path string) (*Config, error) {
	return nil, errNotImplemented
}

// Watch reloads the file on change and calls onChange with every config that validates. A
// rejected reload keeps the previous config, and is logged and counted.
func Watch(ctx context.Context, path string, current *Config, onChange func(*Config)) error {
	return errNotImplemented
}
