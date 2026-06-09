package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
)

// dockerCfgSecret builds a kubernetes.io/dockerconfigjson Secret for one
// registry hostname.
func dockerCfgSecret(registry, username, password string) corev1.Secret {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := map[string]any{
		"auths": map[string]any{
			registry: map[string]any{
				"username": username,
				"password": password,
				"auth":     auth,
			},
		},
	}
	raw, _ := json.Marshal(cfg)
	return corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
}

// resolveOne is a small helper: pull the only matching keychain for an
// image and ask it to Resolve the image's repository, returning the
// resulting Authenticator (Anonymous on miss).
func resolveOne(t *testing.T, image string, secret corev1.Secret) authn.Authenticator {
	t.Helper()
	keychains, err := GetKeychains(image, []corev1.Secret{secret})
	if err != nil {
		t.Fatalf("GetKeychains: %v", err)
	}
	ref, err := name.ParseReference(image)
	if err != nil {
		t.Fatalf("ParseReference: %v", err)
	}
	if len(keychains) == 0 {
		return authn.Anonymous
	}
	auth, err := keychains[0].Resolve(ref.Context())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return auth
}

func mustAuthConfig(t *testing.T, a authn.Authenticator) authn.AuthConfig {
	t.Helper()
	cfg, err := a.Authorization()
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	return *cfg
}

// TestKeychainResolve_DockerHub regression-guards the
// distribution/reference vs go-containerregistry normalization mismatch.
// reference.ParseNormalizedNamed(...).Name() returns "docker.io/...",
// while name.ParseReference(...).Context().Name() returns
// "index.docker.io/...". With the old code, authConfigKeychain.Resolve()
// always returned Anonymous for Docker Hub images even with valid
// credentials.
//
// Note: a secret built with --docker-server=docker.io (bare host, no
// `index.` prefix) is NOT covered. That's an upstream kubelet keyring
// quirk — Lookup() Adds the secret under its own host key, URLsMatchStr
// won't match it against index.docker.io/..., and the default-registry
// fallback only looks up the literal key "index.docker.io". This is
// independent of the keychain bug we're fixing; the operator must use
// `index.docker.io` or `https://index.docker.io/v1/` in the Secret.
func TestKeychainResolve_DockerHub(t *testing.T) {
	cases := []struct {
		name       string
		image      string
		secretHost string
	}{
		{"image-docker.io,secret-index.docker.io", "docker.io/actian/foo:1", "index.docker.io"},
		{"image-docker.io,secret-https://index.docker.io/v1/", "docker.io/actian/foo:1", "https://index.docker.io/v1/"},
		{"image-index.docker.io,secret-index.docker.io", "index.docker.io/actian/foo:1", "index.docker.io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := dockerCfgSecret(tc.secretHost, "u", "p")
			auth := resolveOne(t, tc.image, secret)
			if auth == authn.Anonymous {
				t.Fatalf("expected creds, got Anonymous (%s / %s)", tc.image, tc.secretHost)
			}
			cfg := mustAuthConfig(t, auth)
			if cfg.Username != "u" || cfg.Password != "p" {
				t.Fatalf("wrong creds: %+v", cfg)
			}
		})
	}
}

// TestKeychainResolve_OtherRegistries makes sure the new canonicalization
// (via go-containerregistry/pkg/name) leaves non-docker.io registries
// alone.
func TestKeychainResolve_OtherRegistries(t *testing.T) {
	cases := []struct {
		image      string
		secretHost string
	}{
		{"quay.io/enix/manager:1", "quay.io"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/foo:1", "123456789012.dkr.ecr.us-east-1.amazonaws.com"},
		{"us-docker.pkg.dev/proj/repo/img:1", "us-docker.pkg.dev"},
		{"ghcr.io/owner/img:1", "ghcr.io"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s+%s", tc.image, tc.secretHost), func(t *testing.T) {
			secret := dockerCfgSecret(tc.secretHost, "u", "p")
			auth := resolveOne(t, tc.image, secret)
			if auth == authn.Anonymous {
				t.Fatalf("expected creds, got Anonymous (%s / %s)", tc.image, tc.secretHost)
			}
		})
	}
}

// TestKeychainResolve_DifferentRegistry makes sure a secret for one
// registry does NOT resolve credentials for an unrelated registry.
func TestKeychainResolve_DifferentRegistry(t *testing.T) {
	secret := dockerCfgSecret("quay.io", "u", "p")
	auth := resolveOne(t, "ghcr.io/owner/img:1", secret)
	if auth != authn.Anonymous {
		t.Fatalf("expected Anonymous, got %+v", auth)
	}
}
