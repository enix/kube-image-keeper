package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	registryserver "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetKeychainsPreservesMatchingCredentialOrder(t *testing.T) {
	original := newFallbackKeychain
	t.Cleanup(func() { newFallbackKeychain = original })
	newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
		return nil, errors.New("fallback must not be constructed")
	}

	keychains, err := GetKeychains(context.Background(), "registry.example.com/image:tag", []corev1.Secret{
		dockerConfigSecret("first", "registry.example.com", "first-user", "first-password"),
		dockerConfigSecret("second", "registry.example.com", "second-user", "second-password"),
	})
	if err != nil {
		t.Fatalf("GetKeychains returned an error: %v", err)
	}
	if len(keychains) != 2 {
		t.Fatalf("keychain count = %d, want 2", len(keychains))
	}

	got := make([]string, 0, len(keychains))
	for _, keychain := range keychains {
		authenticator, err := keychain.Resolve(testResource("registry.example.com/image"))
		if err != nil {
			t.Fatalf("keychain Resolve returned an error: %v", err)
		}
		config, err := authenticator.Authorization()
		if err != nil {
			t.Fatalf("authenticator Authorization returned an error: %v", err)
		}
		got = append(got, config.Username+":"+config.Password)
	}

	want := []string{"first-user:first-password", "second-user:second-password"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("credential order = %v, want %v", got, want)
	}
}

func TestGetKeychainsReturnsFallbackConstructorError(t *testing.T) {
	original := newFallbackKeychain
	t.Cleanup(func() { newFallbackKeychain = original })

	sentinel := errors.New("fallback constructor failed")
	newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
		return nil, sentinel
	}

	_, err := GetKeychains(context.Background(), "registry.example.com/image:tag", nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("GetKeychains error = %v, want wrapped fallback error", err)
	}
	if !strings.Contains(err.Error(), "could not construct registry fallback keychain") {
		t.Fatalf("GetKeychains error = %q, want fallback context", err)
	}
}

func TestGetKeychainsUsesFallbackForEmptyOrUnrelatedSecrets(t *testing.T) {
	tests := []struct {
		name    string
		secrets []corev1.Secret
	}{
		{
			name:    "nil secrets",
			secrets: nil,
		},
		{
			name:    "empty secret",
			secrets: []corev1.Secret{{Type: corev1.SecretTypeDockerConfigJson}},
		},
		{
			name: "empty docker config",
			secrets: []corev1.Secret{{
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
				},
			}},
		},
		{
			name:    "unrelated secret",
			secrets: []corev1.Secret{dockerConfigSecret("unrelated", "other.example.com", "user", "password")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := newFallbackKeychain
			t.Cleanup(func() { newFallbackKeychain = original })

			called := false
			newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
				called = true
				return testKeychain{}, nil
			}

			keychains, err := GetKeychains(context.Background(), "registry.example.com/image:tag", tt.secrets)
			if err != nil {
				t.Fatalf("GetKeychains returned an error: %v", err)
			}
			if !called {
				t.Fatal("expected fallback keychain to be constructed")
			}
			if len(keychains) != 1 {
				t.Fatalf("keychain count = %d, want 1", len(keychains))
			}
		})
	}
}

func TestGetKeychainsRejectsMalformedSupportedSecrets(t *testing.T) {
	tests := []struct {
		name   string
		secret corev1.Secret
	}{
		{
			name: "docker config json",
			secret: corev1.Secret{
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":`),
				},
			},
		},
		{
			name: "docker config",
			secret: corev1.Secret{
				Type: corev1.SecretTypeDockercfg,
				Data: map[string][]byte{
					corev1.DockerConfigKey: []byte(`{"registry.example.com":`),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := newFallbackKeychain
			t.Cleanup(func() { newFallbackKeychain = original })

			called := false
			newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
				called = true
				return testKeychain{}, nil
			}

			_, err := GetKeychains(context.Background(), "registry.example.com/image:tag", []corev1.Secret{tt.secret})
			if err == nil {
				t.Fatal("expected malformed Secret error")
			}
			if called {
				t.Fatal("malformed Secret must not fall back to another keychain")
			}
		})
	}
}

func TestRejectedMatchingSecretDoesNotFallBack(t *testing.T) {
	var sawExplicitCredentials atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if username, password, ok := r.BasicAuth(); ok && username == "user" && password == "password" {
			sawExplicitCredentials.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`)
	}))
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	original := newFallbackKeychain
	t.Cleanup(func() { newFallbackKeychain = original })
	fallbackCalled := false
	newFallbackKeychain = func(context.Context) (authn.Keychain, error) {
		fallbackCalled = true
		return nil, errors.New("fallback must not be constructed")
	}

	client := NewClient([]string{host}, nil).WithPullSecrets([]corev1.Secret{
		dockerConfigSecret("explicit", host, "user", "password"),
	})
	_, _, err := client.ReadDescriptor(context.Background(), http.MethodHead, host+"/image:tag")
	if err == nil {
		t.Fatal("expected registry authentication error")
	}
	if code := TransportStatusCode(err); code != http.StatusUnauthorized {
		t.Fatalf("transport status code = %d, want %d", code, http.StatusUnauthorized)
	}
	if fallbackCalled {
		t.Fatal("rejected matching Secret must not fall back to workload identity or anonymous access")
	}
	if !sawExplicitCredentials.Load() {
		t.Fatal("registry did not receive the matching explicit credentials")
	}
}

func TestFallbackKeychainUsesAnonymousForLocalRegistry(t *testing.T) {
	isolatedHome := t.TempDir()
	t.Setenv("HOME", isolatedHome)
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AZURE_CONFIG_DIR", t.TempDir())
	for _, variable := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_ROLE_ARN",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AZURE_CLIENT_ID",
		"AZURE_CLIENT_SECRET",
		"AZURE_TENANT_ID",
		"AZURE_FEDERATED_TOKEN_FILE",
	} {
		t.Setenv(variable, "")
	}

	original := newFallbackKeychain
	t.Cleanup(func() { newFallbackKeychain = original })
	constructed := false
	newFallbackKeychain = func(ctx context.Context) (authn.Keychain, error) {
		constructed = true
		return original(ctx)
	}

	var authenticatedRequests atomic.Int64
	localRegistry := registryserver.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			authenticatedRequests.Add(1)
		}
		localRegistry.ServeHTTP(w, r)
	}))
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	reference := host + "/test/image:latest"
	image, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(image, reference); err != nil {
		t.Fatal(err)
	}
	authenticatedRequests.Store(0)

	client := NewClient([]string{host}, nil)
	if _, err := client.GetDescriptor(context.Background(), reference); err != nil {
		t.Fatalf("anonymous local registry operation failed: %v", err)
	}
	if !constructed {
		t.Fatal("expected the real fallback keychain to be constructed")
	}
	if got := authenticatedRequests.Load(); got != 0 {
		t.Fatalf("authenticated local registry requests = %d, want 0", got)
	}
}

func dockerConfigSecret(name, registry, username, password string) corev1.Secret {
	config, _ := json.Marshal(map[string]any{
		"auths": map[string]any{
			registry: map[string]string{
				"username": username,
				"password": password,
			},
		},
	})
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: config,
		},
	}
}

type testResource string

func (r testResource) String() string {
	return string(r)
}

func (r testResource) RegistryStr() string {
	resource := string(r)
	if slash := strings.IndexByte(resource, '/'); slash >= 0 {
		resource = resource[:slash]
	}
	return resource
}

type testKeychain struct{}

func (testKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return authn.Anonymous, nil
}
