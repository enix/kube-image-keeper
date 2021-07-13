package registry

import (
	"errors"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var Endpoint = "cache-registry-service:5000"
var Protocol = "http://"

func getK8sClientConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}

		return config, nil
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func NewK8sClient(kubeconfig string) (*kubernetes.Clientset, error) {
	config, err := getK8sClientConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func imageExists(ref name.Reference) bool {
	_, err := remote.Head(ref)
	if err != nil {
		return false
	}

	return true
}

func DeleteImage(imageName string) error {
	ref, err := name.ParseReference(Endpoint + "/" + imageName)
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

	digest, err := name.NewDigest(Endpoint + "/" + imageName + "@" + descriptor.Digest.String())

	if err != nil {
		return err
	}

	return remote.Delete(digest)
}

func CacheImage(imageName string) (bool, error) {
	destRef, err := name.ParseReference(Endpoint + "/" + imageName)
	if err != nil {
		return false, err
	}
	sourceRef, err := name.ParseReference(imageName)
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
