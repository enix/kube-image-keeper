package registry

import (
	"crypto/x509"
	"fmt"
	"os"
)

// LoadRootCAPoolFromFiles reads the PEM-encoded CA bundles at the given paths
// and appends them to a copy of the system root certificate pool. Returns nil
// when no path is supplied — callers then keep the default system trust store.
func LoadRootCAPoolFromFiles(paths []string) (*x509.CertPool, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("loading system cert pool: %w", err)
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}

	for _, path := range paths {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading root CA %q: %w", path, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid PEM certificate found in %q", path)
		}
	}

	return pool, nil
}
