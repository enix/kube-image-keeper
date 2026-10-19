package kuik

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const tlsTestRepository = "test/image:latest"

var tlsTestSecrets int

// createCASecret creates a Secret holding pemData under ca.crt in the default
// namespace and returns the caSecretRef that points to it.
func createCASecret(ctx context.Context, pemData []byte) (*corev1.Secret, *config.CASecretRef) {
	tlsTestSecrets++
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: fmt.Sprintf("tls-test-ca-%d", tlsTestSecrets)},
		Data:       map[string][]byte{config.DefaultCASecretKey: pemData},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
	})
	return secret, &config.CASecretRef{Namespace: secret.Namespace, Name: secret.Name, Key: config.DefaultCASecretKey}
}

// tlsConfig is the default config with the given tls host entries.
func tlsConfig(items map[string]config.TLSEntry) *config.Config {
	cfg, err := config.LoadDefault()
	Expect(err).NotTo(HaveOccurred())
	cfg.TLS = items
	return cfg
}

var _ = Describe("Registry TLS settings of the controllers", func() {
	Context("ClusterImageSetAvailability monitoring", func() {
		registryConfig := &config.RegistryMonitoring{Method: http.MethodGet, Timeout: 5 * time.Second}

		check := func(ctx context.Context, cfg *config.Config, reference string) *kuikv1alpha1.MonitoredImage {
			r := &ClusterImageSetAvailabilityReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Config: cfg}
			image := &kuikv1alpha1.MonitoredImage{Image: reference}
			r.performCheck(ctx, image, registryConfig, nil)
			return image
		}

		It("reports Available for a registry whose CA is trusted through a caSecretRef", func(ctx SpecContext) {
			ca := registrytest.NewCA(GinkgoT())
			reg := registrytest.NewTLSRegistry(GinkgoT(), ca)
			reg.Push(GinkgoT(), tlsTestRepository)
			_, ref := createCASecret(ctx, ca.PEM)
			cfg := tlsConfig(map[string]config.TLSEntry{reg.Host(): {CASecretRef: ref}})

			image := check(ctx, cfg, reg.Host()+"/"+tlsTestRepository)

			Expect(image.Status).To(Equal(kuikv1alpha1.ImageAvailabilityAvailable), image.LastError)
		})

		It("reports Unreachable, naming the Secret, when the CA Secret is missing", func(ctx SpecContext) {
			ca := registrytest.NewCA(GinkgoT())
			reg := registrytest.NewTLSRegistry(GinkgoT(), ca)
			reg.Push(GinkgoT(), tlsTestRepository)
			cfg := tlsConfig(map[string]config.TLSEntry{reg.Host(): {
				CASecretRef: &config.CASecretRef{Namespace: "default", Name: "tls-test-missing-ca", Key: config.DefaultCASecretKey},
			}})

			image := check(ctx, cfg, reg.Host()+"/"+tlsTestRepository)

			Expect(image.Status).To(Equal(kuikv1alpha1.ImageAvailabilityUnreachable))
			Expect(image.LastError).To(ContainSubstring("Secret default/tls-test-missing-ca"))
		})

		It("picks up a change of the CA Secret without a restart", func(ctx SpecContext) {
			ca := registrytest.NewCA(GinkgoT())
			reg := registrytest.NewTLSRegistry(GinkgoT(), ca)
			reg.Push(GinkgoT(), tlsTestRepository)
			secret, ref := createCASecret(ctx, registrytest.NewCA(GinkgoT()).PEM)
			cfg := tlsConfig(map[string]config.TLSEntry{reg.Host(): {CASecretRef: ref}})
			reference := reg.Host() + "/" + tlsTestRepository

			Expect(check(ctx, cfg, reference).Status).To(Equal(kuikv1alpha1.ImageAvailabilityUnreachable))

			secret.Data[config.DefaultCASecretKey] = ca.PEM
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())

			image := check(ctx, cfg, reference)
			Expect(image.Status).To(Equal(kuikv1alpha1.ImageAvailabilityAvailable), image.LastError)
		})
	})

	Context("(Cluster)ImageSetMirror", func() {
		reconciler := func(cfg *config.Config) *ImageSetMirrorBaseReconciler {
			r := &ImageSetMirrorBaseReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Config: cfg}
			r.setupPlatforms()
			return r
		}

		It("copies an image from a source to a destination served by registries whose CAs are trusted through caSecretRefs", func(ctx SpecContext) {
			srcCA, destCA := registrytest.NewCA(GinkgoT()), registrytest.NewCA(GinkgoT())
			src := registrytest.NewTLSRegistry(GinkgoT(), srcCA)
			src.Push(GinkgoT(), tlsTestRepository)
			dest := registrytest.NewTLSRegistry(GinkgoT(), destCA)
			_, srcRef := createCASecret(ctx, srcCA.PEM)
			_, destRef := createCASecret(ctx, destCA.PEM)
			r := reconciler(tlsConfig(map[string]config.TLSEntry{
				src.Host():  {CASecretRef: srcRef},
				dest.Host(): {CASecretRef: destRef},
			}))
			mirrors := kuikv1alpha1.Mirrors{{Registry: dest.Host(), Path: "mirror"}}
			to := &kuikv1alpha1.MirrorStatus{Image: dest.Host() + "/mirror/" + tlsTestRepository}

			Expect(r.mirrorImage(ctx, "", mirrors, nil, src.Host()+"/"+tlsTestRepository, to)).To(Succeed())

			Expect(dest.Has("mirror/" + tlsTestRepository)).To(BeTrue())
			Expect(to.MirroredAt).NotTo(BeNil())
		})

		It("accepts an image already in a destination whose CA is trusted through a caSecretRef when the source is unreachable", func(ctx SpecContext) {
			// no tls entry for the source: its certificate is not trusted
			src := registrytest.NewTLSRegistry(GinkgoT(), registrytest.NewCA(GinkgoT()))
			src.Push(GinkgoT(), tlsTestRepository)
			destCA := registrytest.NewCA(GinkgoT())
			dest := registrytest.NewTLSRegistry(GinkgoT(), destCA)
			dest.Push(GinkgoT(), "mirror/"+tlsTestRepository)
			_, destRef := createCASecret(ctx, destCA.PEM)
			r := reconciler(tlsConfig(map[string]config.TLSEntry{dest.Host(): {CASecretRef: destRef}}))
			mirrors := kuikv1alpha1.Mirrors{{Registry: dest.Host(), Path: "mirror"}}
			to := &kuikv1alpha1.MirrorStatus{Image: dest.Host() + "/mirror/" + tlsTestRepository}

			Expect(r.mirrorImage(ctx, "", mirrors, nil, src.Host()+"/"+tlsTestRepository, to)).To(Succeed())

			Expect(to.MirroredAt).NotTo(BeNil())
		})

		It("deletes a mirrored image from a destination whose CA is trusted through a caSecretRef", func(ctx SpecContext) {
			ca := registrytest.NewCA(GinkgoT())
			dest := registrytest.NewTLSRegistry(GinkgoT(), ca)
			digest := dest.Push(GinkgoT(), "mirror/"+tlsTestRepository)
			_, ref := createCASecret(ctx, ca.PEM)
			credentials := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "tls-test-mirror-credentials"}}
			Expect(k8sClient.Create(ctx, credentials)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, credentials)).To(Succeed()) })
			r := reconciler(tlsConfig(map[string]config.TLSEntry{dest.Host(): {CASecretRef: ref}}))
			mirrors := kuikv1alpha1.Mirrors{{
				Registry:         dest.Host(),
				Path:             "mirror",
				CredentialSecret: &kuikv1alpha1.CredentialSecret{Namespace: "default", Name: credentials.Name},
			}}

			Expect(r.cleanupMirror(ctx, dest.Host()+"/mirror/"+tlsTestRepository, "", mirrors)).To(BeTrue())

			// the deletion is by digest; the in-memory registry keeps the tag entry
			Expect(dest.Has("mirror/test/image@" + digest)).To(BeFalse())
		})
	})
})
