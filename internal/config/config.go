// Package config loads, validates and reloads the global config file of docs/v3/spec.md,
// "Global config": one file mounted from a ConfigMap, read by the three processes, reloaded
// in place and rejected whole when it does not parse or validate.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"time"

	"sigs.k8s.io/yaml"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

const (
	// minInterval is the shortest interval accepted for a window or a destination scan.
	minInterval = 5 * time.Second
	// defaultBlock is the key of `registries` every host inherits from.
	defaultBlock = "default"
)

// clusterIDPattern is the OCI tag alphabet without `_`, the suffix separator.
var clusterIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

// Config is the global config file.
type Config struct {
	// ClusterID is appended to every tag an ImageMirror writes. Required.
	ClusterID string `json:"clusterID"`
	// Metrics enables the optional metrics.
	Metrics Metrics `json:"metrics"`
	// Mirror paces the destination an ImageMirror owns.
	Mirror Mirror `json:"mirror"`
	// Webhook configures the admission path.
	Webhook Webhook `json:"webhook"`
	// Registries paces the reads from each registry host.
	Registries Registries `json:"registries"`
	// FallbackAuth is the credentials the reconciler reads an image with when no CR declares
	// any.
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
	// Default is the `default` block, as written.
	Default RegistryPacing
	// Hosts holds the block of each host, keyed by normalised host.
	Hosts map[string]RegistryPacing
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

// specDefaults is the `registries.default` of the spec's example, under the one written.
var specDefaults = Pacing{
	Check: ResolvedWindow{Interval: time.Minute, Timeout: 10 * time.Second},
	Copy:  ResolvedWindow{Interval: 3 * time.Minute, Timeout: 0},
}

// defaults returns a config holding the values of the spec's example, which the file
// overrides key by key.
func defaults() *Config {
	return &Config{
		Mirror: Mirror{DestinationScan: DestinationScan{Interval: Duration{time.Hour}}},
		Webhook: Webhook{
			DemoteMirrorWithPullPolicyAlways: true,
			AvailabilityCheck: AvailabilityCheck{
				Timeout:             Duration{2 * time.Second},
				ActiveCheckCache:    ActiveCheckCache{TTL: Duration{10 * time.Second}},
				DemoteKnownFailures: true,
			},
		},
	}
}

// For returns the pace of a host: its block merged over `default`, field by field, itself
// merged over the spec's defaults.
func (r Registries) For(host string) Pacing {
	p := specDefaults
	r.Default.applyTo(&p)
	if block, ok := r.Hosts[normalizeHost(host)]; ok {
		block.applyTo(&p)
	}
	return p
}

func (b RegistryPacing) applyTo(p *Pacing) {
	b.Check.applyTo(&p.Check)
	b.Copy.applyTo(&p.Copy)
}

func (w *Window) applyTo(r *ResolvedWindow) {
	if w == nil {
		return
	}
	if w.Interval != nil {
		r.Interval = w.Interval.Duration
	}
	if w.Timeout != nil {
		r.Timeout = w.Timeout.Duration
	}
}

// UnmarshalJSON reads `default` and the host blocks, refusing unknown keys in each: a type
// with its own decoder does not inherit the strictness of the outer one.
func (r *Registries) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Hosts = map[string]RegistryPacing{}
	for key, value := range raw {
		var block RegistryPacing
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&block); err != nil {
			return fmt.Errorf("registries.%s: %w", key, err)
		}
		if key == defaultBlock {
			r.Default = block
			continue
		}
		p, err := imagepath.ParsePath(key, imagepath.Group)
		if err != nil || len(p.Segments) > 0 {
			return fmt.Errorf("registries.%s: the key must be a registry host", key)
		}
		host := p.Host
		if _, dup := r.Hosts[host]; dup {
			return fmt.Errorf("registries.%s: host %s is declared twice", key, host)
		}
		r.Hosts[host] = block
	}
	return nil
}

// normalizeHost names a registry host the way normalised references do: index.docker.io is
// docker.io.
func normalizeHost(host string) string {
	p, err := imagepath.ParsePath(host, imagepath.Group)
	if err != nil {
		return host
	}
	return p.Host
}

// Duration is a duration written with its unit (`1m`, `10s`), or `0`.
type Duration struct {
	time.Duration
}

// UnmarshalJSON reads a duration string, or the bare number 0.
func (d *Duration) UnmarshalJSON(data []byte) error {
	if string(data) == "0" {
		d.Duration = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration %s must be written with its unit, such as 10s or 1m", data)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalJSON writes the duration with its unit.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Load reads, defaults and validates the config file. A key the spec does not define, at
// any level, rejects it, as does any invalid value.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading global config: %w", err)
	}
	cfg := defaults()
	if err := yaml.UnmarshalStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing global config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating global config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	switch {
	case c.ClusterID == "":
		errs = append(errs, errors.New("clusterID is required"))
	case !clusterIDPattern.MatchString(c.ClusterID):
		errs = append(errs, fmt.Errorf("clusterID %q must match %s", c.ClusterID, clusterIDPattern))
	}

	errs = append(errs,
		checkInterval("mirror.destinationScan.interval", c.Mirror.DestinationScan.Interval),
		checkPositive("webhook.availabilityCheck.timeout", c.Webhook.AvailabilityCheck.Timeout),
		checkPositive("webhook.availabilityCheck.activeCheckCache.ttl", c.Webhook.AvailabilityCheck.ActiveCheckCache.TTL),
		c.Registries.Default.validate("registries."+defaultBlock),
	)
	for host, block := range c.Registries.Hosts {
		errs = append(errs, block.validate("registries."+host))
	}
	for i, e := range c.FallbackAuth {
		errs = append(errs, validateFallbackAuth(fmt.Sprintf("fallbackAuth[%d]", i), e))
	}
	return errors.Join(errs...)
}

func (b RegistryPacing) validate(key string) error {
	var errs []error
	if b.Check != nil {
		if b.Check.Interval != nil {
			errs = append(errs, checkInterval(key+".check.interval", *b.Check.Interval))
		}
		if b.Check.Timeout != nil {
			errs = append(errs, checkPositive(key+".check.timeout", *b.Check.Timeout))
		}
	}
	if b.Copy != nil {
		if b.Copy.Interval != nil {
			errs = append(errs, checkInterval(key+".copy.interval", *b.Copy.Interval))
		}
		// 0 means no bound: a transfer takes what it takes.
		if b.Copy.Timeout != nil && b.Copy.Timeout.Duration < 0 {
			errs = append(errs, fmt.Errorf("%s.copy.timeout must not be negative", key))
		}
	}
	return errors.Join(errs...)
}

func checkInterval(key string, d Duration) error {
	if d.Duration < minInterval {
		return fmt.Errorf("%s is %s, below the minimum of %s", key, d, minInterval)
	}
	return nil
}

func checkPositive(key string, d Duration) error {
	if d.Duration <= 0 {
		return fmt.Errorf("%s must be positive", key)
	}
	return nil
}

var providerNames = []kuikv1alpha1.ProviderName{kuikv1alpha1.ProviderAWS, kuikv1alpha1.ProviderGCP, kuikv1alpha1.ProviderAzure}

// validateFallbackAuth applies the rule `alternatives` entries follow, one entry at a time:
// mixing forms in the list is allowed.
func validateFallbackAuth(key string, e auth.FallbackAuthEntry) error {
	if _, _, err := imagepath.ValidateEntry(e.Repository, e.RepositoryGroup); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	switch {
	case (e.SecretRef == nil) == (e.Provider == nil):
		return fmt.Errorf("%s: exactly one of secretRef or provider must be set", key)
	case e.SecretRef != nil && e.SecretRef.Name == "":
		return fmt.Errorf("%s.secretRef.name is required", key)
	case e.Provider != nil && !slices.Contains(providerNames, e.Provider.Name):
		return fmt.Errorf("%s.provider.name %q must be one of %v", key, e.Provider.Name, providerNames)
	case e.Provider != nil && e.Provider.ServiceAccountRef != nil && e.Provider.ServiceAccountRef.Name == "":
		return fmt.Errorf("%s.provider.serviceAccountRef.name is required", key)
	}
	return nil
}
