package registry

import (
	"errors"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var Endpoint = "cache-registry-service:5000"
var Protocol = "http://"

func imageExists(ref name.Reference, options ...remote.Option) bool {
	_, err := remote.Head(ref, options...)
	if err != nil {
		return false
	}

	return true
}

func DeleteImage(imageName string) error {
	ref, err := name.ParseReference(Endpoint+"/"+imageName, name.Insecure)
	if err != nil {
		return err
	}

	if !imageExists(ref) {
		return nil
	}

	descriptor, err := remote.Head(ref)
	if err != nil {
		return err
	}

	digest, err := name.NewDigest(Endpoint+"/"+imageName+"@"+descriptor.Digest.String(), name.Insecure)

	if err != nil {
		return err
	}

	return remote.Delete(digest)
}

func CacheImage(imageName string, keychain authn.Keychain) (bool, error) {
	destRef, err := name.ParseReference(Endpoint+"/"+imageName, name.Insecure)
	if err != nil {
		return false, err
	}
	sourceRef, err := name.ParseReference(imageName, name.Insecure)
	if err != nil {
		return false, err
	}

	if imageExists(destRef) {
		return false, nil
	}

	auth := remote.WithAuthFromKeychain(keychain)
	if !imageExists(sourceRef, auth) {
		return false, errors.New("could not find source image")
	}

	image, err := remote.Image(sourceRef, auth)
	if err != nil {
		return false, err
	}

	if err := remote.Write(destRef, image); err != nil {
		return false, err
	}

	if err := remote.Put(destRef, image); err != nil {
		return false, err
	}

	return true, nil
}

func SanitizeName(image string) string {
	nameRegex := regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)
	return strings.Join(nameRegex.FindAllString(image, -1), "-")
}
