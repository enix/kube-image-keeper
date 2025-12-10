package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getPullSecret(ctx context.Context, k8sClient client.Client, namespace, name string, secret *corev1.Secret) error {
	secretReference := client.ObjectKey{Namespace: namespace, Name: name}
	if err := k8sClient.Get(ctx, secretReference, secret); err != nil {
		return err
	}
	return nil
}

func getImageSecretFromMirrors(ctx context.Context, k8sClient client.Client, image, namespace string, mirrors kuikv1alpha1.Mirrors) (*corev1.Secret, error) {
	destCredentialSecret := mirrors.GetCredentialSecretForImage(image)

	if destCredentialSecret == nil {
		return nil, nil
	}

	// This allows to use the same code for both ClusterImageSetMirror and ImageSetMirror
	if namespace == "" {
		namespace = destCredentialSecret.Namespace
	}

	secret := &corev1.Secret{}
	if err := getPullSecret(ctx, k8sClient, namespace, destCredentialSecret.Name, secret); err != nil {
		return nil, err
	}

	return secret, nil
}
