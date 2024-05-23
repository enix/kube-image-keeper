package podutils

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ImagePullSecretNamesFromPod(client client.Client, ctx context.Context, pod *corev1.Pod) ([]string, error) {
	if pod.Spec.ServiceAccountName == "" {
		return []string{}, nil
	}

	var serviceAccount corev1.ServiceAccount
	serviceAccountNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Spec.ServiceAccountName}
	if err := client.Get(ctx, serviceAccountNamespacedName, &serviceAccount); err != nil && !apierrors.IsNotFound(err) {
		return []string{}, err
	}

	imagePullSecrets := append(pod.Spec.ImagePullSecrets, serviceAccount.ImagePullSecrets...)
	imagePullSecretNames := make([]string, len(imagePullSecrets))

	for i, imagePullSecret := range imagePullSecrets {
		imagePullSecretNames[i] = imagePullSecret.Name
	}

	return imagePullSecretNames, nil
}
