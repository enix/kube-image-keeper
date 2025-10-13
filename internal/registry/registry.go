package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
)

type descriptorReader func(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error)

type Client struct {
	insecureRegistries []string
	rootCAs            *x509.CertPool
}

type AuthenticatedClient struct {
	*Client
	pullSecrets []corev1.Secret
}

func NewClient(insecureRegistries []string, rootCAs *x509.CertPool) *Client {
	return &Client{
		insecureRegistries: insecureRegistries,
		rootCAs:            rootCAs,
	}
}

func (c *Client) options(ctx context.Context, ref name.Reference) []remote.Option {
	transport := unauthenticatedTransport(ref.Context().RegistryStr(), c.insecureRegistries, c.rootCAs)
	return []remote.Option{
		remote.WithTransport(transport),
		remote.WithContext(ctx),
	}
}

func (c *Client) WithPullSecrets(pullSecrets []corev1.Secret) *AuthenticatedClient {
	return &AuthenticatedClient{
		Client:      c,
		pullSecrets: pullSecrets,
	}
}

func (a *AuthenticatedClient) ReadDescriptor(httpMethod string, imageName string, timeout time.Duration) (*v1.Descriptor, error) {
	keychains, err := GetKeychains(imageName, a.pullSecrets)
	if err != nil {
		return nil, err
	}

	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return nil, err
	}

	// global timeout for all keychains
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	options := a.options(ctx, sourceRef)
	defer cancel()

	var returnedErr error
	for _, keychain := range keychains {
		opts := append([]remote.Option{remote.WithAuthFromKeychain(keychain)}, options...)
		desc, err := getReader(httpMethod)(sourceRef, opts...)

		if err == nil { // stops at the first success
			return desc, nil
		} else {
			returnedErr = err
		}
	}

	return nil, returnedErr
}

func MirrorImage(from, to string) error {
	return crane.Copy(from, to, func(o *crane.Options) {
		o.Platform, _ = v1.ParsePlatform("amd64")
	})
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
