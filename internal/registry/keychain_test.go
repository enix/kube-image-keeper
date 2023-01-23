package registry

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/docker/cli/cli/config"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockClient struct {
	client.Client
}

var pullSecrets = map[string]corev1.Secret{
	"missing_.dockerconfigjson": {
		Data: map[string][]byte{},
	},
	"invalidJson": {
		Data: map[string][]byte{
			".dockerconfigjson": []byte("invalid"),
		},
	},
	"invalidConfigurationFile": {
		Data: map[string][]byte{
			".dockerconfigjson": []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"00000000\"}}}"),
		},
	},
	"foo": {
		Data: map[string][]byte{
			".dockerconfigjson": []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"bG9naW46cGFzc3dvcmQ=\"}}}"),
		},
	},
	"bar": {
		Data: map[string][]byte{
			".dockerconfigjson": []byte("{\"auths\":{\"localhost:5000\":{\"auth\":\"bG9jYWxsb2dpbjpsb2NhbHBhc3N3b3Jk\"}}}"),
		},
	},
	"foobar": {
		Data: map[string][]byte{
			".dockerconfigjson": []byte("{\"auths\":{\"https://index.docker.io/v1/\":{\"auth\":\"bG9naW46cGFzc3dvcmQ=\"},\"localhost:5000\":{\"auth\":\"bG9jYWxsb2dpbjpsb2NhbHBhc3N3b3Jk\"}}}"),
		},
	},
}

var notFoundError = errors.New("not found")
var _, invalidJsonError = config.LoadFromReader(bytes.NewReader([]byte("invalid")))
var defaultAuthenticator = authn.FromConfig(authn.AuthConfig{
	Username: "login",
	Password: "password",
})
var localAuthenticator = authn.FromConfig(authn.AuthConfig{
	Username: "locallogin",
	Password: "localpassword",
})

func (m mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	secret := obj.(*corev1.Secret)
	s, ok := pullSecrets[key.Name]
	if !ok {
		return notFoundError
	}
	*secret = s
	return nil
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name                  string
		pullSecrets           []string
		imageName             string
		expectedAuthenticator authn.Authenticator
		wantErr               error
	}{
		{
			name:        "Empty",
			pullSecrets: []string{},
		},
		{
			name: "Missing secret",
			pullSecrets: []string{
				"missing",
			},
			wantErr: notFoundError,
		},
		{
			name: "Missing .dockerconfigjson",
			pullSecrets: []string{
				"missing_.dockerconfigjson",
			},
			wantErr: missingDockerConfigJsonError,
		},
		{
			name: "Invalid json",
			pullSecrets: []string{
				"invalidJson",
			},
			wantErr: invalidJsonError,
		},
		{
			name: "Default registry",
			pullSecrets: []string{
				"foo",
			},
			expectedAuthenticator: defaultAuthenticator,
		},
		{
			name: "Local regsitry",
			pullSecrets: []string{
				"bar",
			},
			imageName:             "localhost:5000/alpine",
			expectedAuthenticator: localAuthenticator,
		},
		{
			name: "Multiple secrets",
			pullSecrets: []string{
				"foo",
				"bar",
			},
			expectedAuthenticator: defaultAuthenticator,
		},
		{
			name: "Multiple secrets with local registry",
			pullSecrets: []string{
				"foo",
				"bar",
			},
			imageName:             "localhost:5000/alpine",
			expectedAuthenticator: localAuthenticator,
		},
		{
			name: "Multiple secrets in one .dockerconfigjson",
			pullSecrets: []string{
				"foobar",
			},
			expectedAuthenticator: defaultAuthenticator,
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keychain := NewKubernetesKeychain(mockClient{}, "namespace", tt.pullSecrets)
			if tt.imageName == "" {
				tt.imageName = "alpine"
			}
			ref, err := name.ParseReference(tt.imageName)
			g.Expect(err).To(Succeed())

			auth, err := keychain.Resolve(ref.Context())

			if tt.wantErr == nil {
				g.Expect(err).To(Succeed())
				g.Expect(auth).ToNot(BeNil())

				if tt.expectedAuthenticator == nil {
					g.Expect(auth).To(Equal(authn.Anonymous))
				} else {
					g.Expect(auth).To(Equal(tt.expectedAuthenticator))
				}
			} else {
				g.Expect(err).To(MatchError(tt.wantErr))
				g.Expect(auth).To(BeNil())
			}
		})
	}
}
