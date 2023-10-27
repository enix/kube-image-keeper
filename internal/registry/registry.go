package registry

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/utils/strings/slices"
)

var Endpoint = ""
var Protocol = "http://"

// See https://github.com/kubernetes/apimachinery/blob/v0.20.6/pkg/util/validation/validation.go#L198
var sanitizeNameRegex = regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)

func imageExists(ref name.Reference, options ...remote.Option) (bool, error) {
	_, err := remote.Head(ref, options...)
	if err != nil {
		if errIsImageNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func errIsImageNotFound(err error) bool {
	if err, ok := err.(*transport.Error); ok {
		if err.StatusCode == http.StatusNotFound {
			return true
		}
	}
	return false
}

func getDestinationName(sourceName string) (string, error) {
	sourceRef, err := name.ParseReference(sourceName)
	if err != nil {
		return "", err
	}

	sanitizedRegistryName := strings.ReplaceAll(sourceRef.Context().RegistryStr(), ":", "-")
	fullname := strings.ReplaceAll(sourceRef.Name(), "index.docker.io", "docker.io")
	fullname = strings.ReplaceAll(fullname, sourceRef.Context().RegistryStr(), sanitizedRegistryName)

	return Endpoint + "/" + fullname, nil
}

func parseLocalReference(imageName string) (name.Reference, error) {
	destName, err := getDestinationName(imageName)
	if err != nil {
		return nil, err
	}
	return name.ParseReference(destName, name.Insecure)
}

func ImageIsCached(imageName string) (bool, error) {
	reference, err := parseLocalReference(imageName)
	if err != nil {
		return false, err
	}

	return imageExists(reference)
}

func DeleteImage(imageName string) error {
	ref, err := parseLocalReference(imageName)
	if err != nil {
		return err
	}

	descriptor, err := remote.Head(ref)
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

	return remote.Delete(digest)
}

func CacheImage(imageName string, keychain authn.Keychain, architectures []string, insecureRegistries []string) error {
	destRef, err := parseLocalReference(imageName)
	if err != nil {
		return err
	}
	sourceRef, err := name.ParseReference(imageName)
	if err != nil {
		return err
	}

	auth := remote.WithAuthFromKeychain(keychain)
	opts := []remote.Option{auth}

	if slices.Contains(insecureRegistries, sourceRef.Context().Registry.RegistryStr()) {
		transportOption := remote.WithTransport(&http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		})
		opts = append(opts, transportOption)
	}

	desc, err := remote.Get(sourceRef, opts...)
	if err != nil {
		if errIsImageNotFound(err) {

			return errors.New("could not find source image")
		}
		return err
	}

	switch desc.MediaType {
	case types.OCIImageIndex, types.DockerManifestList:
		index, err := desc.ImageIndex()
		if err != nil {
			return err
		}

		filteredIndex := mutate.RemoveManifests(index, func(desc v1.Descriptor) bool {
			for _, arch := range architectures {
				if arch == desc.Platform.Architecture {
					return false
				}
			}
			return true
		})

		if err := remote.WriteIndex(destRef, filteredIndex); err != nil {
			return err
		}
	default:
		image, err := desc.Image()
		if err != nil {
			return err
		}
		if err := remote.Write(destRef, image); err != nil {
			return err
		}
	}

	return nil
}

func SanitizeName(image string) string {
	return strings.Join(sanitizeNameRegex.FindAllString(strings.ToLower(image), -1), "-")
}

func RepositoryLabel(repositoryName string) string {
	sanitizedName := SanitizeName(repositoryName)

	if len(sanitizedName) > 63 {
		return fmt.Sprintf("%x", sha256.Sum224([]byte(sanitizedName)))
	}

	return sanitizedName
}

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
