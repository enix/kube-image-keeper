package registry

import (
	"fmt"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/registry/credentialprovider/secrets"
	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
)

type authConfigKeychain struct {
	authn.AuthConfig
	repositoryName string
}

func (a *authConfigKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	if target.String() != a.repositoryName {
		return authn.Anonymous, nil
	}
	return authn.FromConfig(a.AuthConfig), nil
}

func GetKeychains(repositoryName string, pullSecrets []corev1.Secret) ([]authn.Keychain, error) {
	if keychains, err := getKeychainsFromSecrets(repositoryName, pullSecrets); err != nil {
		return nil, err
	} else {
		return keychains, nil
	}
}

func getKeychainsFromSecrets(repositoryName string, pullSecrets []corev1.Secret) ([]authn.Keychain, error) {
	keychains := []authn.Keychain{}

	named, err := reference.ParseNormalizedNamed(repositoryName)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse image name: %v", err)
	}

	keyring, err := secrets.MakeDockerKeyring(pullSecrets)
	if err != nil {
		return nil, err
	}

	if keyring == nil {
		return keychains, nil
	}

	creds, _ := keyring.Lookup(named.Name())
	for _, cred := range creds {
		keychains = append(keychains, &authConfigKeychain{
			repositoryName: named.Name(),
			AuthConfig: authn.AuthConfig{
				Username:      cred.Username,
				Password:      cred.Password,
				Auth:          cred.Auth,
				IdentityToken: cred.IdentityToken,
				RegistryToken: cred.RegistryToken,
			},
		})
	}

	return keychains, nil
}
