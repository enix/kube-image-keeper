package v1alpha1ext1

import (
	"regexp"

	"github.com/enix/kube-image-keeper/internal/registry"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *Repository) CompileUpdateFilters() ([]regexp.Regexp, error) {
	regexps := make([]regexp.Regexp, len(r.Spec.UpdateFilters))

	for i, updateFilter := range r.Spec.UpdateFilters {
		r, err := regexp.Compile(updateFilter)
		if err != nil {
			return nil, err
		}
		regexps[i] = *r
	}

	return regexps, nil
}

func (r *Repository) GetPullSecrets(apiReader client.Reader) ([]corev1.Secret, error) {
	pullSecrets, err := registry.GetPullSecrets(apiReader, r.Spec.PullSecretsNamespace, r.Spec.PullSecretNames)
	if err != nil {
		return nil, err
	}

	return pullSecrets, nil
}
