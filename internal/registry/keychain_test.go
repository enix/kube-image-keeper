package registry

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"

	ecrLogin "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/chrismellard/docker-credential-acr-env/pkg/credhelper"
	"github.com/docker/cli/cli/config"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockClient struct {
	client.Client
	produceError bool
	namespace    string
}

var pullSecrets = map[string]corev1.Secret{
	"missing_.dockerconfigjson": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{},
	},
	"missing_.dockercfg": {
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{},
	},
	"invalidSecretType": {
		Type: corev1.SecretTypeBasicAuth,
		Data: map[string][]byte{},
	},
	"invalidJson": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("invalid"),
		},
	},
	"invalidConfigurationFile": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"00000000\"}}}"),
		},
	},
	"foo": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"bG9naW46cGFzc3dvcmQ=\"}}}"),
		},
	},
	"bar": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("{\"auths\":{\"localhost:5000\":{\"auth\":\"bG9jYWxsb2dpbjpsb2NhbHBhc3N3b3Jk\"}}}"),
		},
	},
	"foobar": {
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"bG9naW46cGFzc3dvcmQ=\"},\"localhost:5000\":{\"auth\":\"bG9jYWxsb2dpbjpsb2NhbHBhc3N3b3Jk\"}}}"),
		},
	},
	"dockercfg": {
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte("{\"https://index.docker.io/v1/\":{\"username\":\"login\",\"password\":\"password\"}}"),
		},
	},
}

var clientError = errors.New("an error occurred")
var _, invalidJsonError = config.LoadFromReader(bytes.NewReader([]byte("invalid")))

func (m mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if m.produceError {
		return clientError
	}

	if key.Namespace != m.namespace {
		return apierrors.NewNotFound(schema.GroupResource{}, key.String())
	}

	secret := obj.(*corev1.Secret)
	s, ok := pullSecrets[key.Name]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{}, key.String())
	}
	*secret = s

	return nil
}

func TestResolve(t *testing.T) {
	expectedAuthConfig := authn.AuthConfig{
		Username:      "username",
		Password:      "password",
		Auth:          "auth",
		IdentityToken: "identityToken",
		RegistryToken: "registryToken",
	}
	keychain := authConfigKeychain{
		AuthConfig: expectedAuthConfig,
	}

	g := NewWithT(t)
	auth, err := keychain.Resolve(nil)
	g.Expect(err).To(BeNil())

	authConfig, err := auth.Authorization()
	g.Expect(*authConfig).To(Equal(expectedAuthConfig))
	g.Expect(err).To(BeNil())
}

func TestGetKeychains(t *testing.T) {
	defaultKeychains := []authn.Keychain{
		google.Keychain,
		authn.NewKeychainFromHelper(credhelper.NewACRCredentialsHelper()),
	}
	dockerHubKeychains := append(defaultKeychains, &authConfigKeychain{
		AuthConfig: authn.AuthConfig{
			Username: "login",
			Password: "password",
		},
	})
	localKeychains := append(defaultKeychains, &authConfigKeychain{
		AuthConfig: authn.AuthConfig{
			Username: "locallogin",
			Password: "localpassword",
		},
	})

	tests := []struct {
		name              string
		repositoryName    string
		pullSecrets       []corev1.Secret
		expectedKeychains []authn.Keychain
		wantErr           error
	}{
		{
			name:              "Empty",
			expectedKeychains: defaultKeychains,
		},
		{
			name:              "Empty bis",
			pullSecrets:       []corev1.Secret{{}, {}, {}},
			expectedKeychains: defaultKeychains,
		},
		{
			name: "Multiple secrets (dockerhub)",
			pullSecrets: []corev1.Secret{
				pullSecrets["foo"],
				pullSecrets["bar"],
			},
			expectedKeychains: dockerHubKeychains,
		},
		{
			name:           "Multiple secrets (localhost)",
			repositoryName: "localhost:5000/alpine",
			pullSecrets: []corev1.Secret{
				pullSecrets["foo"],
				pullSecrets["bar"],
			},
			expectedKeychains: localKeychains,
		},
		{
			name: "Missing .dockerconfigjson",
			pullSecrets: []corev1.Secret{
				pullSecrets["missing_.dockerconfigjson"],
			},
			expectedKeychains: defaultKeychains,
		},
		{
			name: "Missing .dockercfg",
			pullSecrets: []corev1.Secret{
				pullSecrets["missing_.dockercfg"],
			},
			expectedKeychains: defaultKeychains,
		},
		{
			name: "Invalid secret type",
			pullSecrets: []corev1.Secret{
				pullSecrets["invalidSecretType"],
			},
			expectedKeychains: defaultKeychains,
		},
		{
			name: "Multiple secrets in one .dockerconfigjson (dockerhub)",
			pullSecrets: []corev1.Secret{
				pullSecrets["foobar"],
			},
			expectedKeychains: dockerHubKeychains,
		},
		{
			name:           "Multiple secrets in one .dockerconfigjson (localhost)",
			repositoryName: "localhost:5000/alpine",
			pullSecrets: []corev1.Secret{
				pullSecrets["foobar"],
			},
			expectedKeychains: localKeychains,
		},
		{
			name: ".dockercfg format",
			pullSecrets: []corev1.Secret{
				pullSecrets["dockercfg"],
			},
			expectedKeychains: dockerHubKeychains,
		},
		{
			name: "Invalid json",
			pullSecrets: []corev1.Secret{
				pullSecrets["invalidJson"],
			},
			wantErr: invalidJsonError,
		},
		{
			name: "Invalid configuration file",
			pullSecrets: []corev1.Secret{
				pullSecrets["invalidConfigurationFile"],
			},
			wantErr: errors.New("unable to parse auth field, must be formatted as base64(username:password)"),
		},
		{
			name:           "Invalid image name",
			repositoryName: ":::://::",
			wantErr:        errors.New("couldn't parse image name: invalid reference format"),
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.repositoryName == "" {
				tt.repositoryName = "alpine"
			}

			keychains, err := GetKeychains(tt.repositoryName, tt.pullSecrets)

			if tt.wantErr == nil {
				g.Expect(err).To(Succeed())
				g.Expect(keychains).To(ConsistOf(tt.expectedKeychains))
			} else {
				g.Expect(err).To(MatchError(tt.wantErr))
				g.Expect(keychains).To(BeNil())
			}
		})
	}

	t.Run("Not from Amazon ECR", func(t *testing.T) {
		keychains, err := GetKeychains("alpine", []corev1.Secret{})
		g.Expect(err).To(BeNil())
		g.Expect(keychains).ToNot(ContainElement(authn.NewKeychainFromHelper(ecrLogin.NewECRHelper())))
	})

	t.Run("From Amazon ECR", func(t *testing.T) {
		keychains, err := GetKeychains("000000000000.dkr.ecr.eu-west-1.amazonaws.com/some-image", []corev1.Secret{})
		g.Expect(err).To(BeNil())
		g.Expect(keychains).To(ContainElement(authn.NewKeychainFromHelper(ecrLogin.NewECRHelper())))
	})
}

func TestGetPullSecrets(t *testing.T) {
	tests := []struct {
		name               string
		pullSecrets        []string
		clientProduceError bool
		wantErr            error
	}{
		{
			name: "Empty",
		},
		{
			name: "Existing secrets",
			pullSecrets: []string{
				"foo",
				"bar",
			},
		},
		{
			name: "Missing secret",
			pullSecrets: []string{
				"missing",
			},
		},
		{
			name: "Some missing, some existing secrets",
			pullSecrets: []string{
				"foo",
				"missing",
				"bar",
				"not_existing",
			},
		},
		{
			name: "Client returns an error",
			pullSecrets: []string{
				"foo",
			},
			clientProduceError: true,
			wantErr:            clientError,
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace := make([]byte, 10)
			rand.Read(namespace)

			apiReader := mockClient{produceError: tt.clientProduceError, namespace: string(namespace)}
			secrets, err := GetPullSecrets(apiReader, string(namespace), tt.pullSecrets)

			expectedSecrets := []corev1.Secret{}
			for _, pullSecretName := range tt.pullSecrets {
				if secret, ok := pullSecrets[pullSecretName]; ok {
					expectedSecrets = append(expectedSecrets, secret)
				}
			}

			if tt.wantErr == nil {
				g.Expect(err).To(Succeed())
				g.Expect(secrets).To(ConsistOf(expectedSecrets))
			} else {
				g.Expect(err).To(MatchError(tt.wantErr))
				g.Expect(secrets).To(BeNil())
			}
		})
	}
}
