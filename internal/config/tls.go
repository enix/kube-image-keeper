package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// DefaultCAKey is the key of a CA Secret a caSecretRef that names none reads.
const DefaultCAKey = "ca.crt"

// TLS is the `tls` key: how kuik verifies the certificate of each registry host, keyed by
// normalised host. A key matches a request host exactly, port included.
type TLS map[string]TLSEntry

// TLSEntry is the TLS setting of one host. A CR entry with `insecure: true` ignores it.
type TLSEntry struct {
	// CASecretRef names the Secret whose PEM bundle is added to the system trust store, for
	// this host only.
	CASecretRef *CASecretRef `json:"caSecretRef,omitempty"`
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// CASecretRef is a key of a Secret in the cluster resource namespace.
type CASecretRef struct {
	Name string `json:"name"`
	// Key holds a PEM bundle of one or more certificates. Default: ca.crt.
	Key string `json:"key,omitempty"`
}

// UnmarshalJSON reads the host entries, refusing unknown keys in each, and keys them by host
// as `registries` does: lowercased, index.docker.io as docker.io.
func (t *TLS) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	entries := TLS{}
	for key, value := range raw {
		var entry TLSEntry
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entry); err != nil {
			return fmt.Errorf("tls.%s: %w", key, err)
		}
		host, err := hostKey("tls", key)
		if err != nil {
			return err
		}
		if _, dup := entries[host]; dup {
			return fmt.Errorf("tls.%s: host %s is declared twice", key, host)
		}
		if entry.CASecretRef != nil && entry.CASecretRef.Key == "" {
			entry.CASecretRef.Key = DefaultCAKey
		}
		entries[host] = entry
	}
	*t = entries
	return nil
}

func (t TLS) validate() error {
	var errs []error
	for host, entry := range t {
		switch {
		case entry.CASecretRef != nil && entry.InsecureSkipVerify:
			errs = append(errs, fmt.Errorf("tls.%s: caSecretRef and insecureSkipVerify are exclusive", host))
		case entry.CASecretRef != nil && entry.CASecretRef.Name == "":
			errs = append(errs, fmt.Errorf("tls.%s.caSecretRef.name is required", host))
		}
	}
	return errors.Join(errs...)
}
