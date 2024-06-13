package v1alpha1

import (
	"context"
	"strings"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *CachedImage) Repository() (reference.Named, error) {
	named, err := reference.ParseNormalizedNamed(r.Spec.SourceImage)
	if err != nil {
		return nil, err
	}

	return named, nil
}

func (r *CachedImage) GetPullSecrets(apiReader client.Reader) ([]corev1.Secret, error) {
	named, err := r.Repository()
	if err != nil {
		return nil, err
	}

	repository := Repository{}
	err = apiReader.Get(context.TODO(), types.NamespacedName{Name: registry.SanitizeName(named.Name())}, &repository)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	pullSecrets, err := repository.GetPullSecrets(apiReader)
	if err != nil {
		return nil, err
	}

	return pullSecrets, nil
}

func CachedImageNameFromSourceImage(sourceImage string) (string, error) {
	ref, err := reference.ParseAnyReference(sourceImage)
	if err != nil {
		return "", err
	}

	sanitizedName := registry.SanitizeName(ref.String())
	if !strings.Contains(sourceImage, ":") {
		sanitizedName += "-latest"
	}

	return sanitizedName, nil
}
