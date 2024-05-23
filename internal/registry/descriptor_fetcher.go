package registry

import (
	"crypto/x509"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
)

type DescriptorFetcher struct {
	InsecureRegistries []string
	RootCAs            *x509.CertPool
}

func (d *DescriptorFetcher) Get(imageName string, pullSecrets []corev1.Secret) (*remote.Descriptor, error) {
	return GetDescriptor(imageName, pullSecrets, d.InsecureRegistries, d.RootCAs)
}
