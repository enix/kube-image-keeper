package registry

import (
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func ContainerAnnotationKey(containerName string, initContainer bool) string {
	template := "original-image-%s"
	if initContainer {
		template = "original-init-image-%s"
	}

	if len(containerName)+len(template)-2 > 63 {
		containerName = fmt.Sprintf("%x", sha1.Sum([]byte(containerName)))
	}

	return fmt.Sprintf(template, containerName)
}

func GetDescriptor(imageName string, insecureRegistries []string, rootCAs *x509.CertPool) (*remote.Descriptor, error) {
	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return nil, err
	}

	anonymousKeychain := authn.NewMultiKeychain()
	opts := options(sourceRef, anonymousKeychain, insecureRegistries, rootCAs)
	return remote.Get(sourceRef, opts...)
}

func options(ref name.Reference, keychain authn.Keychain, insecureRegistries []string, rootCAs *x509.CertPool) []remote.Option {
	auth := remote.WithAuthFromKeychain(keychain)
	opts := []remote.Option{auth}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs}

	if slices.Contains(insecureRegistries, ref.Context().Registry.RegistryStr()) {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	opts = append(opts, remote.WithTransport(transport))

	return opts
}
