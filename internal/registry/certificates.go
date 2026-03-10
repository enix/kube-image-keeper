package registry

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

func LoadRootCAPoolFromFiles(certificatePaths []string) (*x509.CertPool, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	for _, path := range certificatePaths {
		cleanPath := filepath.Clean(path)

		info, err := os.Stat(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("cannot access certificate file %s: %w", cleanPath, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("certificate path %s is not a regular file", cleanPath)
		}

		caCert, err := os.ReadFile(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read certificate file %s: %w", cleanPath, err)
		}
		if !rootCAs.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to add certificate from %s", cleanPath)
		}
	}

	return rootCAs, nil
}
