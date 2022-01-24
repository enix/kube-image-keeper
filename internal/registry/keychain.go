package registry

import (
	"bytes"
	"context"
	"errors"
	"sync"

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
	client      client.Client
	mu          sync.Mutex
	namespace   string
	pullSecrets []string
}

func NewKubernetesKeychain(client client.Client, namespace string, pullSecrets []string) authn.Keychain {
	return &kubernetesKeychain{
		client:      client,
		namespace:   namespace,
		pullSecrets: pullSecrets,
	}
}

// Resolve implements Keychain.
func (k *kubernetesKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if len(k.pullSecrets) == 0 {
		return authn.Anonymous, nil
	}

	var secret corev1.Secret
	err := k.client.Get(context.TODO(), types.NamespacedName{
		Namespace: k.namespace,
		Name:      k.pullSecrets[0], // TODO: support multiple pull secrets
	}, &secret)
	if err != nil {
		return nil, err
	}

	dockerConfigJson, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return nil, errors.New("invalid secret: missing .dockerconfigjson key")
	}
	cf, err := config.LoadFromReader(bytes.NewReader(dockerConfigJson))
	if err != nil {
		return nil, err
	}

	// See:
	// https://github.com/google/ko/issues/90
	// https://github.com/moby/moby/blob/fc01c2b481097a6057bec3cd1ab2d7b4488c50c4/registry/config.go#L397-L404
	key := target.RegistryStr()
	if key == name.DefaultRegistry {
		key = DefaultAuthKey
	}

	cfg, err := cf.GetAuthConfig(key)
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
