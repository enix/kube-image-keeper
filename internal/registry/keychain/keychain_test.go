package keychain

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/go-containerregistry/pkg/authn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	alice = "alice"
	host  = "registry.example.com"
)

// auths maps a registry key of a docker config to the username of its credential.
type auths map[string]string

func dockerConfig(entries auths) map[string]any {
	config := map[string]any{}
	for key, username := range entries {
		config[key] = map[string]string{
			"auth": base64.StdEncoding.EncodeToString([]byte(username + ":password")),
		}
	}
	return config
}

func dockerConfigJSONSecret(name string, entries auths) corev1.Secret {
	data, err := json.Marshal(map[string]any{"auths": dockerConfig(entries)})
	Expect(err).NotTo(HaveOccurred())
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kuik-system", Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: data},
	}
}

func dockercfgSecret(name string, entries auths) corev1.Secret {
	data, err := json.Marshal(dockerConfig(entries))
	Expect(err).NotTo(HaveOccurred())
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kuik-system", Name: name},
		Type:       corev1.SecretTypeDockercfg,
		Data:       map[string][]byte{corev1.DockerConfigKey: data},
	}
}

// usernames returns the username each authenticator presents, in order.
func usernames(authenticators []authn.Authenticator) []string {
	names := make([]string, 0, len(authenticators))
	for _, authenticator := range authenticators {
		config, err := authenticator.Authorization()
		Expect(err).NotTo(HaveOccurred())
		names = append(names, config.Username)
	}
	return names
}

var _ = Describe("Authenticators", func() {
	It("returns the credential of a Secret whose registry covers the image", func() {
		secrets := []corev1.Secret{dockerConfigJSONSecret("a", auths{host: alice})}

		authenticators, err := Authenticators("registry.example.com/team/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(Equal([]string{alice}))
	})

	It("returns no credential when no Secret covers the image", func() {
		secrets := []corev1.Secret{dockerConfigJSONSecret("a", auths{host: alice})}

		authenticators, err := Authenticators("other.example.com/team/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(authenticators).To(BeEmpty())
	})

	It("orders the matching credentials from the longest path to the shortest", func() {
		secrets := []corev1.Secret{dockerConfigJSONSecret("a", auths{
			host:                             "host",
			"registry.example.com/team":      "team",
			"registry.example.com/team/app":  "app",
			"registry.example.com/elsewhere": "elsewhere",
		})}

		authenticators, err := Authenticators("registry.example.com/team/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(Equal([]string{"app", "team", "host"}))
	})

	It("matches a registry key with a glob in its host", func() {
		secrets := []corev1.Secret{dockerConfigJSONSecret("a", auths{"*.example.com": "wildcard"})}

		authenticators, err := Authenticators("registry.example.com/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(Equal([]string{"wildcard"}))
	})

	It("applies an index.docker.io credential to a short Docker Hub name such as nginx", func() {
		secrets := []corev1.Secret{dockerConfigJSONSecret("a", auths{"https://index.docker.io/v1/": "hub"})}

		authenticators, err := Authenticators("nginx", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(Equal([]string{"hub"}))
	})

	It("returns one credential per Secret when two Secrets cover the same registry", func() {
		secrets := []corev1.Secret{
			dockerConfigJSONSecret("a", auths{host: alice}),
			dockerConfigJSONSecret("b", auths{host: "bob"}),
		}

		authenticators, err := Authenticators("registry.example.com/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(ConsistOf(alice, "bob"))
	})

	It("reads a legacy kubernetes.io/dockercfg Secret", func() {
		secrets := []corev1.Secret{dockercfgSecret("a", auths{host: "legacy"})}

		authenticators, err := Authenticators("registry.example.com/app:v1", secrets)

		Expect(err).NotTo(HaveOccurred())
		Expect(usernames(authenticators)).To(Equal([]string{"legacy"}))
	})

	It("returns a MalformedSecretError for a Secret whose content is not a docker config", func() {
		secret := dockerConfigJSONSecret("broken", auths{})
		secret.Data[corev1.DockerConfigJsonKey] = []byte("not a docker config")

		_, err := Authenticators("registry.example.com/app:v1", []corev1.Secret{secret})

		var malformed *MalformedSecretError
		Expect(errors.As(err, &malformed)).To(BeTrue())
		Expect(malformed.Name).To(Equal("broken"))
	})
})
