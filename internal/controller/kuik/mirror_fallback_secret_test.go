package kuik

import (
	"context"
	"testing"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Regression for the architectural gap: once the webhook rewrites
// consumers to pull from the mirror, no pod carries the original
// docker.io/... reference and getPullSecretsFromPods yields no
// credentials. The mirror controller must fall back to a chart-level
// Secret keyed by upstream registry host so private upstreams (Docker
// Hub, private quay.io, etc.) can still be mirrored.
func TestGetMirroringFallbackSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const ns = "kuik-system"

	dockerhub := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "dockerhub-image-pull", Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dockerhub).Build()

	mkRec := func(reg map[string]config.MirrorRegistry) *ImageSetMirrorBaseReconciler {
		return &ImageSetMirrorBaseReconciler{
			Client: client,
			Scheme: scheme,
			Config: &config.Config{
				Mirroring: config.Mirroring{Registries: reg},
			},
		}
	}

	cases := []struct {
		name     string
		image    string
		registry map[string]config.MirrorRegistry
		want     *corev1.Secret
	}{
		{
			name:  "docker.io image resolves via index.docker.io host key",
			image: "docker.io/actian/vault-utils:main.222556b",
			registry: map[string]config.MirrorRegistry{
				"index.docker.io": {
					FallbackCredentialSecret: &kuikv1alpha1.CredentialSecret{
						Name: "dockerhub-image-pull", Namespace: ns,
					},
				},
			},
			want: dockerhub,
		},
		{
			name:  "index.docker.io image resolves",
			image: "index.docker.io/actian/vault-utils:main.222556b",
			registry: map[string]config.MirrorRegistry{
				"index.docker.io": {
					FallbackCredentialSecret: &kuikv1alpha1.CredentialSecret{
						Name: "dockerhub-image-pull", Namespace: ns,
					},
				},
			},
			want: dockerhub,
		},
		{
			name:  "library/short-name image resolves via Docker Hub default host",
			image: "ubuntu:24.04",
			registry: map[string]config.MirrorRegistry{
				"index.docker.io": {
					FallbackCredentialSecret: &kuikv1alpha1.CredentialSecret{
						Name: "dockerhub-image-pull", Namespace: ns,
					},
				},
			},
			want: dockerhub,
		},
		{
			name:  "no entry for the host returns nil (no fallback)",
			image: "quay.io/enix/manager:1",
			registry: map[string]config.MirrorRegistry{
				"index.docker.io": {
					FallbackCredentialSecret: &kuikv1alpha1.CredentialSecret{
						Name: "dockerhub-image-pull", Namespace: ns,
					},
				},
			},
			want: nil,
		},
		{
			name:     "empty registries map returns nil",
			image:    "docker.io/actian/vault-utils:main.222556b",
			registry: nil,
			want:     nil,
		},
		{
			name:  "host entry present but FallbackCredentialSecret unset returns nil",
			image: "docker.io/actian/vault-utils:main.222556b",
			registry: map[string]config.MirrorRegistry{
				"index.docker.io": {},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mkRec(tc.registry)
			got, err := r.getMirroringFallbackSecret(context.Background(), tc.image)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch {
			case tc.want == nil && got == nil:
				// ok
			case tc.want == nil && got != nil:
				t.Fatalf("expected nil, got Secret %s/%s", got.Namespace, got.Name)
			case tc.want != nil && got == nil:
				t.Fatalf("expected Secret %s/%s, got nil", tc.want.Namespace, tc.want.Name)
			case got.Name != tc.want.Name || got.Namespace != tc.want.Namespace:
				t.Fatalf("expected %s/%s, got %s/%s",
					tc.want.Namespace, tc.want.Name, got.Namespace, got.Name)
			}
		})
	}
}

// Sanity-check that an image whose upstream is the configured host but
// whose Secret entry points at a Secret that doesn't exist surfaces the
// API error, rather than silently swallowing it (we want the reconciler
// to retry, not to skip mirroring).
func TestGetMirroringFallbackSecret_MissingSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &ImageSetMirrorBaseReconciler{
		Client: client,
		Scheme: scheme,
		Config: &config.Config{
			Mirroring: config.Mirroring{
				Registries: map[string]config.MirrorRegistry{
					"index.docker.io": {
						FallbackCredentialSecret: &kuikv1alpha1.CredentialSecret{
							Name: "missing", Namespace: "kuik-system",
						},
					},
				},
			},
		},
	}
	if _, err := r.getMirroringFallbackSecret(context.Background(), "docker.io/foo/bar:1"); err == nil {
		t.Fatal("expected error for missing Secret, got nil")
	}
}
