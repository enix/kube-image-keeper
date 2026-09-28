// Package keychain turns docker config Secrets into registry credentials, with the
// kubelet's own resolution.
package keychain

import (
	"errors"
	"fmt"

	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"

	"github.com/enix/kube-image-keeper/internal/registry/credentialprovider/secrets"
)

// MalformedSecretError reports a Secret whose content is not a docker config.
type MalformedSecretError struct {
	Namespace string
	Name      string
	Err       error
}

func (e *MalformedSecretError) Error() string {
	return "secret " + e.Namespace + "/" + e.Name + " is not a docker config: " + e.Err.Error()
}

func (e *MalformedSecretError) Unwrap() error { return e.Err }

// Authenticators returns one authenticator per credential of secrets that covers image,
// the most specific path first, as the kubelet resolves them. A Secret of another type
// than kubernetes.io/dockerconfigjson or kubernetes.io/dockercfg is ignored, as the kubelet
// ignores it. A malformed Secret is a *MalformedSecretError.
func Authenticators(image string, pullSecrets []corev1.Secret) ([]authn.Authenticator, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, fmt.Errorf("parsing image %q: %w", image, err)
	}

	for _, secret := range pullSecrets {
		if err := validate(secret); err != nil {
			return nil, &MalformedSecretError{Namespace: secret.Namespace, Name: secret.Name, Err: err}
		}
	}

	keyring, err := secrets.MakeDockerKeyring(pullSecrets)
	if err != nil {
		return nil, err
	}
	if keyring == nil {
		return nil, nil
	}

	creds, _ := keyring.Lookup(named.Name())
	authenticators := make([]authn.Authenticator, 0, len(creds))
	for _, cred := range creds {
		authenticators = append(authenticators, authn.FromConfig(authn.AuthConfig{
			Username:      cred.Username,
			Password:      cred.Password,
			Auth:          cred.Auth,
			IdentityToken: cred.IdentityToken,
			RegistryToken: cred.RegistryToken,
		}))
	}
	return authenticators, nil
}

// validate checks that a docker config Secret holds a docker config the keyring can read.
func validate(secret corev1.Secret) error {
	keys := map[corev1.SecretType]string{
		corev1.SecretTypeDockerConfigJson: corev1.DockerConfigJsonKey,
		corev1.SecretTypeDockercfg:        corev1.DockerConfigKey,
	}
	key, ok := keys[secret.Type]
	if !ok {
		return nil
	}
	if len(secret.Data[key]) == 0 {
		return errors.New("key " + key + " is missing or empty")
	}
	_, err := secrets.MakeDockerKeyring([]corev1.Secret{secret})
	return err
}
