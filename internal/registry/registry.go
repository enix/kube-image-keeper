package registry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

var Endpoint = ""
var Protocol = "http://"

// See https://github.com/kubernetes/apimachinery/blob/v0.20.6/pkg/util/validation/validation.go#L198
var sanitizeNameRegex = regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)

func imageExists(ref name.Reference, options ...remote.Option) (bool, error) {
	_, err := remote.Head(ref, options...)
	if err != nil {
		if err, ok := err.(*transport.Error); ok {
			if err.StatusCode == http.StatusNotFound {
				return false, nil
			}
		}
		return false, err
	}

	return true, nil
}

func getDestinationName(sourceName string) (string, error) {
	sourceRef, err := name.ParseReference(sourceName, name.Insecure)
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

	if exists, err := imageExists(ref); !exists || err != nil {
		return err
	}

	descriptor, err := remote.Head(ref)
	if err != nil {
		return err
	}

	digest, err := name.NewDigest(ref.Name()+"@"+descriptor.Digest.String(), name.Insecure)
	if err != nil {
		return err
	}

	return remote.Delete(digest)
}

func CacheImage(imageName string, keychain authn.Keychain) error {
	destRef, err := parseLocalReference(imageName)
	if err != nil {
		return err
	}
	sourceRef, err := name.ParseReference(imageName, name.Insecure)
	if err != nil {
		return err
	}

	auth := remote.WithAuthFromKeychain(keychain)
	exists, err := imageExists(sourceRef, auth)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("could not find source image")
	}

	image, err := remote.Image(sourceRef, auth)
	if err != nil {
		return err
	}

	if err := remote.Write(destRef, image); err != nil {
		return err
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
