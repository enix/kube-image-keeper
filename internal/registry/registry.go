package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

type descriptorReader func(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error)

type Client struct {
	insecureRegistries []string
	rootCAs            *x509.CertPool
	timeout            time.Duration
	pullSecrets        []corev1.Secret
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

func (c *Client) WithTimeout(timeout time.Duration) *Client {
	c.timeout = timeout
	return c
}

func (c *Client) WithPullSecrets(pullSecrets []corev1.Secret) *Client {
	c.pullSecrets = pullSecrets
	return c
}

// Execute execute a callback with authentication with options including authentication and optional timeout
func (c *Client) Execute(imageName string, action func(ref name.Reference, opts ...remote.Option) error) error {
	keychains, err := GetKeychains(imageName, c.pullSecrets)
	if err != nil {
		return err
	}

	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return err
	}

	ctx := context.Background()

	if c.timeout > 0 {
		// global timeout for all keychains
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer func() {
			cancel()
		}()
	}

	options := c.options(ctx, sourceRef)

	if len(keychains) == 0 {
		keychains = append(keychains, authn.DefaultKeychain)
	}

	var errs []error
	for _, keychain := range keychains {
		opts := append([]remote.Option{remote.WithAuthFromKeychain(keychain)}, options...)
		err := action(sourceRef, opts...)

		if err == nil { // stops at the first success
			return nil
		} else {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (c *Client) ReadDescriptor(httpMethod string, imageName string) (*v1.Descriptor, error) {
	var desc *v1.Descriptor
	return desc, c.Execute(imageName, func(ref name.Reference, opts ...remote.Option) (err error) {
		desc, err = getReader(httpMethod)(ref, opts...)
		return err
	})
}

func (c *Client) GetDescriptor(imageName string) (*remote.Descriptor, error) {
	var desc *remote.Descriptor
	return desc, c.Execute(imageName, func(ref name.Reference, opts ...remote.Option) (err error) {
		desc, err = remote.Get(ref, opts...)
		return err
	})
}

func (c *Client) CopyImage(src *remote.Descriptor, dest string, architectures []string) error {
	return c.Execute(dest, func(destRef name.Reference, opts ...remote.Option) (err error) {
		switch src.MediaType {
		case types.OCIImageIndex, types.DockerManifestList:
			index, err := src.ImageIndex()
			if err != nil {
				return err
			}

			filteredIndex := mutate.RemoveManifests(index, func(src v1.Descriptor) bool {
				return !slices.ContainsFunc(architectures, func(arch string) bool {
					return src.Platform.Satisfies(v1.Platform{
						Architecture: arch,
					})
				})
			})

			if err := remote.WriteIndex(destRef, filteredIndex, opts...); err != nil {
				return err
			}
		default:
			image, err := src.Image()
			if err != nil {
				return err
			}
			if err := remote.Write(destRef, image, opts...); err != nil {
				return err
			}
		}

		return nil
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

func GetPullSecretsFromPod(ctx context.Context, c client.Client, pod *corev1.Pod) ([]corev1.Secret, error) {
	secrets := []corev1.Secret{}
	for _, imagePullSecret := range pod.Spec.ImagePullSecrets {
		secret := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: imagePullSecret.Name}, secret); err != nil {
			return nil, fmt.Errorf("could not get image pull secret %q: %w", imagePullSecret.Name, err)
		}
		secrets = append(secrets, *secret)
	}

	return secrets, nil
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
