package registry

import (
	"fmt"

	"github.com/enix/kube-image-keeper/internal/registry/credentialprovider/secrets"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
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

	// Use go-containerregistry/pkg/name to canonicalize the repository name
	// so that Resolve()'s strict equality below matches what
	// remote.{Get,Write,Head} hands us as target.String(). The previous
	// implementation used distribution/reference, which normalizes Docker
	// Hub as "docker.io/..." while go-containerregistry normalizes it as
	// "index.docker.io/..." — the mismatch caused Resolve() to always
	// return Anonymous for Docker Hub images, regardless of valid creds in
	// the pullSecrets keyring.
	ref, err := name.ParseReference(repositoryName)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse image name: %v", err)
	}
	canonicalName := ref.Context().Name()

	keyring, err := secrets.MakeDockerKeyring(pullSecrets)
	if err != nil {
		return nil, err
	}

	if keyring == nil {
		return keychains, nil
	}

	creds, _ := keyring.Lookup(canonicalName)
	for _, cred := range creds {
		keychains = append(keychains, &authConfigKeychain{
			repositoryName: canonicalName,
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
