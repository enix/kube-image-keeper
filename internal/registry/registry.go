package registry

import (
	"errors"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var Endpoint = "cache-registry-service:5000"
var Protocol = "http://"

func imageExists(ref name.Reference) bool {
	_, err := remote.Head(ref)
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

func CacheImage(imageName string) (bool, error) {
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
	if !imageExists(sourceRef) {
		return false, errors.New("could not find source image")
	}

	image, err := remote.Image(sourceRef)
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
