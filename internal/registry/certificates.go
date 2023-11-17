package registry

import (
	"crypto/x509"
	"fmt"
	"os"
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
		caCert, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !rootCAs.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to add certificate from %s", path)
		}
	}

	return rootCAs, nil
}
