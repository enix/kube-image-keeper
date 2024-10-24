package registry

import (
	"context"
	"fmt"

	ecrLogin "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/credentialprovider"
	credentialprovidersecrets "k8s.io/kubernetes/pkg/credentialprovider/secrets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type authConfigKeychain struct {
	authn.AuthConfig
}

func (a *authConfigKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(a.AuthConfig), nil
}

func GetKeychains(repositoryName string, pullSecrets []corev1.Secret) ([]authn.Keychain, error) {
	defaultKeyring := &credentialprovider.BasicDockerKeyring{}

	keyring, err := credentialprovidersecrets.MakeDockerKeyring(pullSecrets, defaultKeyring)
	if err != nil {
		return nil, err
	}

	keychains := []authn.Keychain{}

	named, err := reference.ParseNormalizedNamed(repositoryName)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse image name: %v", err)
	}

	creds, _ := keyring.Lookup(named.Name())
	for _, cred := range creds {
		keychains = append(keychains, &authConfigKeychain{
			AuthConfig: authn.AuthConfig{
				Username:      cred.Username,
				Password:      cred.Password,
				Auth:          cred.Auth,
				IdentityToken: cred.IdentityToken,
				RegistryToken: cred.RegistryToken,
			},
		})
	}

	keychains = append(keychains, authn.NewKeychainFromHelper(ecrLogin.NewECRHelper()))
	keychains = append(keychains, google.Keychain)

	return keychains, nil
}

func GetPullSecrets(apiReader client.Reader, namespace string, pullSecretNames []string) ([]corev1.Secret, error) {
	pullSecrets := []corev1.Secret{}
	for _, pullSecretName := range pullSecretNames {
		var pullSecret corev1.Secret
		err := apiReader.Get(context.TODO(), types.NamespacedName{
			Namespace: namespace,
			Name:      pullSecretName,
		}, &pullSecret)

		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			} else {
				return nil, err
			}
		}

		pullSecrets = append(pullSecrets, pullSecret)
	}

	return pullSecrets, nil
}
