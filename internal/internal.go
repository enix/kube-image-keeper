package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/cespare/xxhash"
	"github.com/distribution/reference"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

func RegistryMonitorNameFromRegistry(registry string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(registry))
}

func RegistryMonitorNameFromReference(image string) (string, error) {
	registry, _, err := RegistryNameFromReference(image)
	if err != nil {
		return "", err
	}

	return RegistryMonitorNameFromRegistry(registry), nil
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
