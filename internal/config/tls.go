package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/distribution/reference"
)

// DefaultCASecretKey is the key of a CA Secret read when caSecretRef sets none.
const DefaultCASecretKey = "ca.crt"

// dockerHub and legacyDockerHub name Docker Hub, as references do.
const (
	dockerHub       = "docker.io"
	legacyDockerHub = "index.docker.io"
)

// setDefaults fills the defaults the koanf struct defaults cannot reach, as
// they do not apply to the entries of a map.
func (t TLS) setDefaults() {
	for host, entry := range t {
		if entry.CASecretRef != nil && entry.CASecretRef.Key == "" {
			entry.CASecretRef.Key = DefaultCASecretKey
			t[host] = entry
		}
	}
}

// Hosts returns the entries keyed by host as the requests name it: lowercased,
// docker.io for index.docker.io. Two keys naming the same host fail Validate;
// Hosts keeps one of them.
func (t TLS) Hosts() map[string]TLSEntry {
	hosts := make(map[string]TLSEntry, len(t))
	for key, entry := range t {
		hosts[NormalizeTLSHost(key)] = entry
	}
	return hosts
}

// NormalizeTLSHost names a host as a tls key does: lowercased, docker.io for
// index.docker.io.
func NormalizeTLSHost(host string) string {
	host = strings.ToLower(host)
	if host == legacyDockerHub {
		return dockerHub
	}
	return host
}

func (t TLS) validate() error {
	errs := make([]error, 0, 3*len(t))
	seen := make(map[string]bool, len(t))

	for _, key := range slices.Sorted(maps.Keys(t)) {
		entry := t[key]
		path := fmt.Sprintf("tls[%s]", key)
		if err := validateTLSHost(key); err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid host key: %w", path, err))
		}
		host := NormalizeTLSHost(key)
		if seen[host] {
			errs = append(errs, fmt.Errorf("%s: host %s is declared twice", path, host))
		}
		seen[host] = true
		if entry.CASecretRef != nil && entry.InsecureSkipVerify {
			errs = append(errs, fmt.Errorf("%s: caSecretRef and insecureSkipVerify are exclusive", path))
		}
		errs = append(errs, entry.CASecretRef.validate(path))
	}

	return errors.Join(errs...)
}

func (r *CASecretRef) validate(path string) error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.Namespace == "" {
		errs = append(errs, fmt.Errorf("%s.caSecretRef.namespace: must not be empty", path))
	}
	if r.Name == "" {
		errs = append(errs, fmt.Errorf("%s.caSecretRef.name: must not be empty", path))
	}
	return errors.Join(errs...)
}

// validateTLSHost checks that a key is a host[:port], with no scheme and no
// path: the registry part of an image reference, as its grammar parses it.
func validateTLSHost(host string) error {
	if host == "" {
		return errors.New("must not be empty")
	}
	host = strings.ToLower(host)
	named, err := reference.ParseNormalizedNamed(host + "/x")
	if err != nil {
		return errors.New("must be host[:port], with no scheme and no path")
	}
	if domain := reference.Domain(named); domain != NormalizeTLSHost(host) {
		return fmt.Errorf("is not a registry host: an image reference reads it as %s", domain)
	}
	if port := portOf(host); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port %q is out of range", port)
		}
	}
	return nil
}

// portOf returns the port of a host[:port], bracketed IPv6 addresses included.
func portOf(host string) string {
	i := strings.LastIndex(host, ":")
	if i < 0 || strings.Contains(host[i:], "]") {
		return ""
	}
	return host[i+1:]
}
