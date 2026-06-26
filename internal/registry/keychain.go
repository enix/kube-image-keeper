package registry

import (
	"fmt"

	"github.com/distribution/reference"
	"github.com/enix/kube-image-keeper/internal/registry/credentialprovider/secrets"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google" // Native GCR/GAR authentication
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

// GetKeychains returns keychains derived from pull secrets. When no secrets are
// provided, it falls back to ambient cloud credential chains (GCP, then the library default).
func GetKeychains(repositoryName string, pullSecrets []corev1.Secret) ([]authn.Keychain, error) {
	// Execute the existing internal logic to parse K8s secrets
	keychains, err := getKeychainsFromSecrets(repositoryName, pullSecrets)
	if err != nil {
		return nil, err
	}

	// CRITICAL FIX: If no explicit K8s secrets are tied to this mirror,
	// fall back to ambient cloud chains instead of leaving the slice empty.
	if len(keychains) == 0 {
		keychains = append(keychains,
			google.Keychain,       // Tries GKE Workload Identity / Application Default Credentials
			authn.DefaultKeychain, // Falls back to local dockercfg env logic
		)
	}

	return keychains, nil
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
