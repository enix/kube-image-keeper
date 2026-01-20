package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	corev1 "k8s.io/api/core/v1"
)

type descriptorReader func(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error)

type Client struct {
	insecureRegistries []string
	rootCAs            *x509.CertPool
	timeout            time.Duration
	pullSecrets        []corev1.Secret
	headerCapture      *HeaderCapture
}

func NewClient(insecureRegistries []string, rootCAs *x509.CertPool) *Client {
	return &Client{
		insecureRegistries: insecureRegistries,
		rootCAs:            rootCAs,
		headerCapture:      &HeaderCapture{},
	}
}

func (c *Client) newTransportOption(ref name.Reference) remote.Option {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: c.rootCAs}

	if slices.Contains(c.insecureRegistries, ref.Context().RegistryStr()) {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	c.headerCapture.roundTripper = transport

	return remote.WithTransport(c.headerCapture)
}

func (c *Client) newContextOption(ctx context.Context) (opt remote.Option, cancel func()) {
	if c.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
	}

	return remote.WithContext(ctx), cancel
}

func (c *Client) WithTimeout(timeout time.Duration) *Client {
	c.timeout = timeout
	return c
}

func (c *Client) WithPullSecrets(pullSecrets []corev1.Secret) *Client {
	// TODO: rename into WithCredentialSecrets
	c.pullSecrets = pullSecrets
	return c
}

// Execute execute a callback options including authentication and an optional timeout
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
	transportOption := c.newTransportOption(sourceRef)

	if len(keychains) == 0 {
		keychains = append(keychains, authn.DefaultKeychain)
	}

	var errs []error
	for _, keychain := range keychains {
		contextOption, cancel := c.newContextOption(ctx)
		defer cancel()

		opts := []remote.Option{
			remote.WithAuthFromKeychain(keychain),
			transportOption,
			contextOption,
		}
		err := action(sourceRef, opts...)

		if err == nil { // stops at the first success
			return nil
		} else {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (c *Client) ReadDescriptor(httpMethod string, imageName string) (desc *v1.Descriptor, h http.Header, err error) {
	err = c.Execute(imageName, func(ref name.Reference, opts ...remote.Option) (e error) {
		desc, e = getReader(httpMethod)(ref, opts...)
		return e
	})
	return desc, c.headerCapture.GetLastHeaders(), err
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

func (c *Client) DeleteImage(imageName string) error {
	return c.Execute(imageName, func(ref name.Reference, opts ...remote.Option) (err error) {
		descriptor, err := remote.Head(ref, opts...)
		if err != nil {
			if errIsImageNotFound(err) {
				return nil
			}
			return err
		}

		digest, err := name.NewDigest(ref.Name()+"@"+descriptor.Digest.String(), name.Insecure)
		if err != nil {
			return err
		}
		return remote.Delete(digest, opts...)
	})
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

func errIsImageNotFound(err error) bool {
	if err, ok := err.(*transport.Error); ok {
		if err.StatusCode == http.StatusNotFound {
			return true
		}
	}
	return false
}
