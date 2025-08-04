package registry

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/strings/slices"
)

var Endpoint = ""
var Protocol = "http://"

var ErrNotFound = errors.New("could not find source image")

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

func options(ref name.Reference, keychain authn.Keychain, insecureRegistries []string, rootCAs *x509.CertPool) []remote.Option {
	auth := remote.WithAuthFromKeychain(keychain)
	opts := []remote.Option{auth}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs}

	if slices.Contains(insecureRegistries, ref.Context().Registry.RegistryStr()) {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	opts = append(opts, remote.WithTransport(transport))

	return opts
}

func ImageIsCached(imageName string) (bool, error) {
	reference, err := parseLocalReference(imageName)
	if err != nil {
		return false, err
	}

	exists, err := imageExists(reference)
	if err != nil {
		err = fmt.Errorf("could not determine if the image present in cache: %w", err)
	}
	return exists, err
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

// Perform an image caching, and update the caching progress
// onProgressUpdate: Local cache registry write progress update call back. The total size written to the cache registry may be less then the total size of the image, if there are duplicated or existing layer already.
// onUpdateTotalSize: Total image size callback at the end of the caching. Size of all layers will be included, regardless whether they are already in cache registry.
func CacheImage(imageName string, desc *remote.Descriptor, architectures []string, onProgressUpdate func(v1.Update), onUpdateTotalSize func(int64)) error {

	destRef, err := parseLocalReference(imageName)
	if err != nil {
		return err
	}

	progressUpdate := make(chan v1.Update, 100)
	// The channel will be closed by remote.Write / remote.WriteIndex call with remote.WithProgress option.
	
	go func() {
		for update := range progressUpdate {
			if onProgressUpdate != nil {
				onProgressUpdate(update)
			}
		}
	}()

	switch desc.MediaType {
	case types.OCIImageIndex, types.DockerManifestList:
		index, err := desc.ImageIndex()
		if err != nil {
			close(progressUpdate)
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

		if err := remote.WriteIndex(destRef, filteredIndex, remote.WithProgress(progressUpdate)); err != nil {
			return err
		}
		
		if onUpdateTotalSize != nil {
			// Calculate total compressed size for image blobs
			totalSize, err := getImageSizeByManifestIndex(filteredIndex)
			if err != nil {
				return nil
			}

			onUpdateTotalSize(totalSize)
		}
	default:
		image, err := desc.Image()
		if err != nil {
			close(progressUpdate)
			return err
		}
		if err := remote.Write(destRef, image, remote.WithProgress(progressUpdate)); err != nil {
			return err
		}

		if onUpdateTotalSize != nil {
			var totalSize int64

			// We will ignore the size of the manifest, as well as the size of config file.
			// Only blob size is calculated.
			// The code snippet to include config and manifest file size is being kept here for future reference.
			/*

				manifestSize, err := image.Size()
				if err != nil {
					return nil
				}
				totalSize += manifestSize
				config, err := image.Manifest()
				if err != nil {
					return nil
				}
				totalSize += config.Config.Size
			*/

			// Get layers and calculate total size
			layers, err := image.Layers()
			if err != nil {
				return nil // Ignore
			}

			for _, layer := range layers {
				size, err := layer.Size()
				if err != nil {
					return nil
				}
				totalSize += size
			}

			onUpdateTotalSize(totalSize)
		}
	}

	return nil
}

func GetLocalDescriptor(imageName string) (*remote.Descriptor, error) {
	ref, err := parseLocalReference(imageName)
	if err != nil {
		return nil, err
	}

	descriptor, err := remote.Get(ref)
	if err != nil {
		return nil, err
	}

	return descriptor, nil
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

	var cacheErrors []error
	for _, keychain := range keychains {
		opts := options(sourceRef, keychain, insecureRegistries, rootCAs)
		desc, err := remote.Get(sourceRef, opts...)

		if err == nil { // stops at the first success
			return desc, nil
		} else if errIsImageNotFound(err) {
			err = ErrNotFound
		}
		cacheErrors = append(cacheErrors, err)
	}

	return nil, utilerrors.NewAggregate(cacheErrors)
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

// Calculate total compressed layer blob size by given Image manifest
func getImageSizeByImageManifest(im v1.Image) (int64, error) {
	var totalSize int64
	totalSize = 0

	// We will ignore the size of manifest file here. The code snippet is list below for information.
	/*
		manifestSize, err := im.Size()
		if err != nil {
			return 0, err
		}
		totalSize += manifestSize
	*/

	layers, err := im.Layers()
	if err != nil {
		return 0, err
	}
	for _, layer := range layers {
		layerSize, err := layer.Size()
		if err != nil {
			return 0, err
		}
		totalSize += layerSize
	}
	return totalSize, nil
}

// Calculate total compressed layer blob size by given ImageIndex
// Reference: https://github.com/google/go-containerregistry/blob/59a4b85930392a30c39462519adc8a2026d47181/pkg/v1/remote/pusher.go#L380
func getImageSizeByManifestIndex(tt v1.ImageIndex) (int64, error) {
	var totalSize int64
	totalSize = 0

	// We will ignore the size of manifest file here. The code snippet is list below for information.
	/*
		manifestSize, err := tt.Size()
		if err != nil {
			return 0, err
		}
		totalSize += manifestSize
	*/
	children, err := partial.Manifests(tt)
	if err != nil {
		return 0, err
	}

	for _, child := range children {
		switch typedChild := child.(type) {
		case v1.ImageIndex:
			size, err := getImageSizeByManifestIndex(typedChild)
			if err != nil {
				return 0, err
			}
			totalSize += size

		case v1.Image:
			imageSize, err := getImageSizeByImageManifest(typedChild)
			if err != nil {
				return 0, err
			}
			totalSize += imageSize

		case v1.Layer:
			layerSize, err := typedChild.Size()
			if err != nil {
				return 0, err
			}
			totalSize += layerSize
		}
	}

	return totalSize, nil
}
