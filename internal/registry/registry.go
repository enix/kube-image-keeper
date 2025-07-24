package registry

import (
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"time"

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

func HealthCheck(registry string, insecureRegistries []string, rootCAs *x509.CertPool) error {
	client := &http.Client{
		Transport: unauthenticatedTransport(registry, insecureRegistries, rootCAs),
		Timeout:   5 * time.Second, // TODO: make this configurable
	}

	url := "https://" + registry + "/v2/"

	resp, err := client.Head(url)
	if err != nil {
		return err
	}

	_ = resp.Body.Close()

	if slices.Contains([]int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode) {
		return nil
	}
	return fmt.Errorf("unexpected status: %d", resp.StatusCode)
}

func unauthenticatedTransport(registry string, insecureRegistries []string, rootCAs *x509.CertPool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs}

	if slices.Contains(insecureRegistries, registry) {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	return transport
}

func options(ref name.Reference, keychain authn.Keychain, insecureRegistries []string, rootCAs *x509.CertPool) []remote.Option {
	transport := unauthenticatedTransport(ref.Context().RegistryStr(), insecureRegistries, rootCAs)
	return []remote.Option{
		remote.WithAuthFromKeychain(keychain),
		remote.WithTransport(transport),
	}
}
