package config

// TLS is the `tls` key: how kuik verifies the certificate of each registry host, keyed by
// normalised host.
type TLS map[string]TLSEntry

// TLSEntry is the TLS setting of one host.
type TLSEntry struct {
	// CASecretRef names the Secret whose PEM bundle is added to the system trust store.
	CASecretRef *CASecretRef `json:"caSecretRef,omitempty"`
	// InsecureSkipVerify keeps HTTPS without verifying the certificate.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// CASecretRef is a key of a Secret in the cluster resource namespace.
type CASecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

// UnmarshalJSON is a stub.
func (t *TLS) UnmarshalJSON([]byte) error { return nil }
