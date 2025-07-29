package registry

import (
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

var ErrNotFound = errors.New("could not find source image")

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

func GetDescriptor(imageName string, pullSecrets []corev1.Secret, insecureRegistries []string, rootCAs *x509.CertPool) (*remote.Descriptor, error) {
	keychains, err := GetKeychains(imageName, pullSecrets)
	if err != nil {
		return nil, err
	}

	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return nil, err
	}

	errs := []error{}
	for _, keychain := range keychains {
		opts := options(sourceRef, keychain, insecureRegistries, rootCAs)
		desc, err := remote.Get(sourceRef, opts...)

		if err == nil { // stops at the first success
			return desc, nil
		} else if errIsImageNotFound(err) {
			err = ErrNotFound
		}
		errs = append(errs, err)
	}

	return nil, utilerrors.NewAggregate(errs)
}

func HealthCheck(registry string, insecureRegistries []string, rootCAs *x509.CertPool) error {
	client := &http.Client{
		Transport: unauthenticatedTransport(registry, insecureRegistries, rootCAs),
		Timeout:   5 * time.Second, // TODO: make this configurable
	}

	// TODO: support http:// too
	url := "https://" + registry + "/v2/"

	// TODO: use HEAD by default, and make it configurable (ghcr.io does not support HEAD)
	resp, err := client.Get(url)
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

func errIsImageNotFound(err error) bool {
	if err, ok := err.(*transport.Error); ok {
		if err.StatusCode == http.StatusNotFound {
			return true
		}
	}
	return false
}
