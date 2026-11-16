// Package testerconfig loads the static configuration read at start.
package testerconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// defaultExpireAfter is how long a seen image or a logged request is kept
// without being seen again.
const defaultExpireAfter = 7 * 24 * time.Hour

// Duration is a time.Duration read from a Go duration string ("168h",
// "90m"), from a whole number of days ("7d"), or from the number 0.
type Duration time.Duration

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalJSON implements json.Unmarshaler. A null leaves d unchanged.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if string(b) == "0" {
		*d = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expireAfter must be a duration such as 168h, 90m or 7d, or 0: %s", b)
	}
	v, err := parseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// parseDuration parses a Go duration or <n>d (days, an integer, possibly
// negative so that validation can name it).
func parseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseInt(n, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("expireAfter: invalid duration %q: want a whole number of days", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("expireAfter: invalid duration %q: want 168h, 90m, 7d or 0", s)
	}
	return v, nil
}

// ProxyHost forwards a host label to a real registry. Name is a host label or
// a path.Match pattern without a dot (mirror-*).
type ProxyHost struct {
	Name    string `json:"name"`
	Backend string `json:"backend"`
	// URL is Backend, parsed by Resolve. Callers never modify it.
	URL *url.URL `json:"-"`
}

// Config is the static configuration.
type Config struct {
	BaseDomain     string      `json:"baseDomain"`
	RegistryAddr   string      `json:"registryAddr"`
	AdminAddr      string      `json:"adminAddr"`
	PlainHTTPAddr  string      `json:"plainHTTPAddr"`
	PlainHTTPHost  string      `json:"plainHTTPHost"`
	TLSCertFile    string      `json:"tlsCertFile"`
	TLSKeyFile     string      `json:"tlsKeyFile"`
	Proxies        []ProxyHost `json:"proxies"`
	TrustedProxies []string    `json:"trustedProxies"`
	StatePath      string      `json:"statePath"`
	LogSize        int         `json:"logSize"`
	// ExpireAfter is how long seen images, their generations and digests,
	// and logged requests are kept without being seen again; 0 keeps them
	// forever.
	ExpireAfter Duration `json:"expireAfter"`

	// TrustedNets is TrustedProxies, parsed by Resolve.
	TrustedNets []*net.IPNet `json:"-"`
}

// defaults returns the configuration a file overrides: a key the file omits
// (or sets to null) keeps its value here.
func defaults() Config {
	return Config{
		RegistryAddr:  ":8080",
		AdminAddr:     ":8081",
		PlainHTTPHost: "http",
		LogSize:       10000,
		ExpireAfter:   Duration(defaultExpireAfter),
	}
}

// Load reads the configuration file at file over the defaults, resolves it
// and validates it. It calls Resolve and the validation itself: a Config it
// returns can be used as is. Every problem of the document is reported at
// once, each with its key path.
func Load(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", file, err)
	}
	c := defaults()
	if err := yaml.UnmarshalStrict(b, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", file, err)
	}
	if err := errors.Join(c.Resolve(), c.validate()); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", file, err)
	}
	return &c, nil
}

// Resolve normalises the names and parses what the rest of the program
// reads parsed: ProxyHost.URL and Config.TrustedNets. Load calls it; code
// that builds a Config by hand calls it before handing the Config on. It
// can run twice, with the same result, and reports every backend or CIDR
// that does not parse, with its key path.
func (c *Config) Resolve() error {
	var errs []error
	c.BaseDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(c.BaseDomain)), ".")
	c.PlainHTTPHost = strings.ToLower(strings.TrimSpace(c.PlainHTTPHost))
	for i := range c.Proxies {
		p := &c.Proxies[i]
		p.Name = strings.ToLower(strings.TrimSpace(p.Name))
		u, err := parseBackend(p.Backend)
		if err != nil {
			errs = append(errs, fmt.Errorf("proxies[%d].backend %q %w", i, p.Backend, err))
			u = nil
		}
		p.URL = u
	}
	c.TrustedNets = nil
	for i, s := range c.TrustedProxies {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("trustedProxies[%d] %q: %w", i, s, err))
			continue
		}
		c.TrustedNets = append(c.TrustedNets, n)
	}
	return errors.Join(errs...)
}

// parseBackend parses a proxy backend: scheme://host[:port], http or https.
func parseBackend(backend string) (*url.URL, error) {
	u, err := url.Parse(backend)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("must be an http(s) URL")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.User != nil || u.Fragment != "" {
		return nil, errors.New("must be scheme://host[:port]")
	}
	return u, nil
}

// validate reports every problem Resolve does not: required keys, label
// shapes, duplicates. Only Load calls it, so configurations built by hand
// (without addresses or logSize) stay usable.
func (c *Config) validate() error {
	var errs []error
	if c.BaseDomain == "" {
		errs = append(errs, errors.New("baseDomain is required"))
	}
	if c.RegistryAddr == "" {
		errs = append(errs, errors.New("registryAddr is required"))
	}
	if c.AdminAddr == "" {
		errs = append(errs, errors.New("adminAddr is required"))
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		errs = append(errs, errors.New("tlsCertFile and tlsKeyFile go together"))
	}
	if c.PlainHTTPHost == "" {
		errs = append(errs, errors.New("plainHTTPHost is required"))
	} else if strings.Contains(c.PlainHTTPHost, ".") {
		errs = append(errs, fmt.Errorf("plainHTTPHost %q must be a single host label", c.PlainHTTPHost))
	}
	if c.LogSize < 1 {
		errs = append(errs, fmt.Errorf("logSize must be positive, got %d", c.LogSize))
	}
	if c.ExpireAfter < 0 {
		errs = append(errs, fmt.Errorf("expireAfter must not be negative, got %s", c.ExpireAfter.Std()))
	}
	first := map[string]int{}
	for i, p := range c.Proxies {
		if p.Name == "" || strings.Contains(p.Name, ".") {
			errs = append(errs, fmt.Errorf("proxies[%d].name %q must be a single host label or a pattern without a dot", i, p.Name))
			continue
		}
		if _, err := path.Match(p.Name, ""); err != nil {
			errs = append(errs, fmt.Errorf("proxies[%d].name %q: %w", i, p.Name, err))
			continue
		}
		if j, dup := first[p.Name]; dup {
			errs = append(errs, fmt.Errorf("proxies[%d].name %q duplicates proxies[%d].name", i, p.Name, j))
			continue
		}
		first[p.Name] = i
	}
	return errors.Join(errs...)
}

// Proxy returns the proxy declared for a host label, if any: the exact name
// first, then the first name that matches as a pattern.
func (c *Config) Proxy(label string) (ProxyHost, bool) {
	for _, p := range c.Proxies {
		if p.Name == label {
			return p, true
		}
	}
	for _, p := range c.Proxies {
		if ok, _ := path.Match(p.Name, label); ok {
			return p, true
		}
	}
	return ProxyHost{}, false
}
