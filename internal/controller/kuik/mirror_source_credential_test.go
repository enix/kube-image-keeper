package kuik

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These tests cover the source-credential fallback that fixes issue #606:
// once the webhook has rewritten consumers to pull from the mirror, no pod
// carries the original image, so the mirror controller must resolve the
// upstream credential from a matching (Cluster)ReplicatedImageSet upstream.
var _ = Describe("Mirror source credential resolution (issue #606)", func() {
	ctx := context.Background()

	const (
		dockerImage = "docker.io/library/nginx:1.25"
		quayImage   = "quay.io/prometheus/node-exporter:v1.8.0"
	)

	newReconciler := func() *ImageSetMirrorBaseReconciler {
		return &ImageSetMirrorBaseReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	}

	makeSecret := func(name, namespace string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"https://index.docker.io/v1/":{"auth":"dXNlcjpwYXNz"}}}`),
			},
		}
	}

	dockerUpstream := func(secretName, secretNamespace string) kuikv1alpha1.ReplicatedUpstream {
		up := kuikv1alpha1.ReplicatedUpstream{
			ImageReference: kuikv1alpha1.ImageReference{Registry: "docker.io"},
			ImageFilter:    kuikv1alpha1.ImageFilterDefinition{Include: []string{".*"}},
		}
		if secretName != "" {
			up.CredentialSecret = &kuikv1alpha1.CredentialSecret{Name: secretName, Namespace: secretNamespace}
		}
		return up
	}

	makeCRIS := func(name string, upstreams ...kuikv1alpha1.ReplicatedUpstream) *kuikv1alpha1.ClusterReplicatedImageSet {
		return &kuikv1alpha1.ClusterReplicatedImageSet{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: kuikv1alpha1.ClusterReplicatedImageSetSpec{
				ReplicatedImageSetSpec: kuikv1alpha1.ReplicatedImageSetSpec{Upstreams: upstreams},
			},
		}
	}

	makeRIS := func(name, namespace string, upstreams ...kuikv1alpha1.ReplicatedUpstream) *kuikv1alpha1.ReplicatedImageSet {
		return &kuikv1alpha1.ReplicatedImageSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       kuikv1alpha1.ReplicatedImageSetSpec{Upstreams: upstreams},
		}
	}

	create := func(obj client.Object) {
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, obj)).To(Succeed()) })
	}

	Context("ClusterReplicatedImageSet (cluster-scoped source)", func() {
		It("resolves the source secret from a matching upstream", func() {
			create(makeSecret("cris-docker-creds", "default"))
			create(makeCRIS("cris-docker", dockerUpstream("cris-docker-creds", "default")))

			got, err := newReconciler().getSourceSecretFromReplicatedImageSets(ctx, dockerImage, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(BeNil())
			Expect(got.Name).To(Equal("cris-docker-creds"))
			Expect(got.Namespace).To(Equal("default"))
		})

		It("returns nil when no upstream matches the image", func() {
			create(makeSecret("cris-docker-creds2", "default"))
			create(makeCRIS("cris-docker-only", dockerUpstream("cris-docker-creds2", "default")))

			got, err := newReconciler().getSourceSecretFromReplicatedImageSets(ctx, quayImage, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("returns nil when the matching upstream declares no CredentialSecret", func() {
			create(makeCRIS("cris-no-cred", dockerUpstream("", "")))

			got, err := newReconciler().getSourceSecretFromReplicatedImageSets(ctx, dockerImage, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})

	Context("ReplicatedImageSet (namespaced source)", func() {
		It("resolves the secret in the RIS namespace for a namespaced ImageSetMirror", func() {
			create(makeSecret("ris-docker-creds", "default"))
			// CredentialSecret.Namespace ("ignored") must be overridden with
			// the RIS' own namespace for namespaced resources.
			create(makeRIS("ris-docker", "default", dockerUpstream("ris-docker-creds", "ignored")))

			got, err := newReconciler().getSourceSecretFromReplicatedImageSets(ctx, dockerImage, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(BeNil())
			Expect(got.Name).To(Equal("ris-docker-creds"))
			Expect(got.Namespace).To(Equal("default"))
		})

		It("does not consult namespaced ReplicatedImageSets for a cluster-scoped mirror (namespace empty)", func() {
			create(makeSecret("ris-isolated-creds", "default"))
			create(makeRIS("ris-isolated", "default", dockerUpstream("ris-isolated-creds", "default")))

			got, err := newReconciler().getSourceSecretFromReplicatedImageSets(ctx, dockerImage, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})

	Context("matchUpstreamCredentialSecret", func() {
		upstreams := []kuikv1alpha1.ReplicatedUpstream{
			{
				ImageReference:   kuikv1alpha1.ImageReference{Registry: "quay.io"},
				ImageFilter:      kuikv1alpha1.ImageFilterDefinition{Include: []string{".*"}},
				CredentialSecret: &kuikv1alpha1.CredentialSecret{Name: "quay"},
			},
			{
				ImageReference:   kuikv1alpha1.ImageReference{Registry: "docker.io"},
				ImageFilter:      kuikv1alpha1.ImageFilterDefinition{Include: []string{".*"}},
				CredentialSecret: &kuikv1alpha1.CredentialSecret{Name: "docker"},
			},
		}

		It("returns the credential of the first matching upstream", func() {
			cs := matchUpstreamCredentialSecret(upstreams, dockerImage)
			Expect(cs).NotTo(BeNil())
			Expect(cs.Name).To(Equal("docker"))
		})

		It("returns nil when nothing matches", func() {
			Expect(matchUpstreamCredentialSecret(upstreams, "ghcr.io/foo/bar:latest")).To(BeNil())
		})
	})
})
