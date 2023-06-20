package registry

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	ecrLogin "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/docker/cli/cli/config"
	dockerCliTypes "github.com/docker/cli/cli/config/types"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultAuthKey is the key used for dockerhub in config files, which
	// is hardcoded for historical reasons.
	DefaultAuthKey = "https://" + name.DefaultRegistry + "/v1/"
)

type kubernetesKeychain struct {
	client     client.Client
	mu         sync.Mutex
	namespace  string
	pullSecret string
}

func NewKubernetesKeychain(client client.Client, namespace string, pullSecrets []string) authn.Keychain {
	keychains := []authn.Keychain{}
	for _, pullSecret := range pullSecrets {
		keychains = append(keychains, &kubernetesKeychain{
			client:     client,
			namespace:  namespace,
			pullSecret: pullSecret,
		})
	}

	// Add ECR Login Helper
	keychains = append(keychains, authn.NewKeychainFromHelper(ecrLogin.NewECRHelper()))

	return authn.NewMultiKeychain(keychains...)
}

// Resolve implements Keychain.
func (k *kubernetesKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	var secret corev1.Secret
	err := k.client.Get(context.TODO(), types.NamespacedName{
		Namespace: k.namespace,
		Name:      k.pullSecret,
	}, &secret)
	if err != nil {
		return nil, err
	}

	secretKey := ""
	if secret.Type == corev1.SecretTypeDockerConfigJson {
		secretKey = corev1.DockerConfigJsonKey
	} else if secret.Type == corev1.SecretTypeDockercfg {
		secretKey = corev1.DockerConfigKey
	} else {
		return nil, fmt.Errorf("invalid secret type (%s)", secret.Type)
	}
	dockerConfigJson, ok := secret.Data[secretKey]
	if !ok {
		return nil, fmt.Errorf("invalid secret: missing %s key", secretKey)
	}
	cf, err := config.LoadFromReader(bytes.NewReader(dockerConfigJson))
	if err != nil {
		return nil, err
	}

	// See:
	// https://github.com/google/ko/issues/90
	// https://github.com/moby/moby/blob/fc01c2b481097a6057bec3cd1ab2d7b4488c50c4/registry/config.go#L397-L404
	authKey := target.RegistryStr()
	if authKey == name.DefaultRegistry {
		authKey = DefaultAuthKey
	}

	cfg, err := cf.GetAuthConfig(authKey)
	if err != nil {
		return nil, err
	}

	empty := dockerCliTypes.AuthConfig{}
	if cfg == empty {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username:      cfg.Username,
		Password:      cfg.Password,
		Auth:          cfg.Auth,
		IdentityToken: cfg.IdentityToken,
		RegistryToken: cfg.RegistryToken,
	}), nil
}
