package registry

import (
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
)

type descriptorReader func(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error)

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

func ReadDescriptor(httpMethod string, imageName string, pullSecrets []corev1.Secret, insecureRegistries []string, rootCAs *x509.CertPool) (*v1.Descriptor, error) {
	keychains, err := GetKeychains(imageName, pullSecrets)
	if err != nil {
		return nil, err
	}

	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return nil, err
	}

	var returnedErr error
	for _, keychain := range keychains {
		opts := options(sourceRef, keychain, insecureRegistries, rootCAs)
		desc, err := getReader(httpMethod)(sourceRef, opts...)

		if err == nil { // stops at the first success
			return desc, nil
		} else {
			returnedErr = err
		}
	}

	return nil, returnedErr
}

func ImageNameFromReference(image string) (string, error) {
	ref, err := reference.ParseAnyReference(image)
	if err != nil {
		return "", err
	}

	image = ref.String()
	if !strings.Contains(image, ":") {
		image += "-latest"
	}

	h := xxhash.Sum64String(image)

	return fmt.Sprintf("%016x", h), nil
}

func RegistryNameFromReference(image string) (string, string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", "", err
	}

	parts := strings.SplitN(named.String(), "/", 2)
	return parts[0], parts[1], nil
}

func getReader(httpMethod string) descriptorReader {
	switch httpMethod {
	case http.MethodGet:
		return getDescriptor
	case http.MethodHead:
		return remote.Head
	default:
		panic(fmt.Sprintf("unsupported http method (%s)", httpMethod))
	}
}

func getDescriptor(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error) {
	desc, err := remote.Get(ref, options...)
	if err != nil {
		return nil, err
	}
	return &desc.Descriptor, nil
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
