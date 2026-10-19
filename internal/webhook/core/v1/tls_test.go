package v1

import (
	"time"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
	"github.com/maypok86/otter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go4.org/syncutil/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Registry TLS settings of the webhook", func() {
	const repository = "test/image:latest"

	// defaulter is a PodCustomDefaulter with the given tls host entries and the
	// caches SetupPodWebhookWithManager would build.
	defaulter := func(items map[string]config.TLSEntry) *PodCustomDefaulter {
		cfg, err := config.LoadDefault()
		Expect(err).NotTo(HaveOccurred())
		cfg.Routing.ActiveCheck.Timeout = 5 * time.Second
		cfg.TLS = items
		checkCache, err := otter.MustBuilder[string, bool](10).
			Cost(func(key string, value bool) uint32 { return 1 }).
			WithTTL(time.Second).
			Build()
		Expect(err).NotTo(HaveOccurred())
		return &PodCustomDefaulter{
			Client:           k8sClient,
			Config:           cfg,
			checkCache:       checkCache,
			requestGroup:     &singleflight.Group{},
			cleanupSemaphore: make(chan struct{}, 1),
		}
	}

	It("finds an alternative available on a registry whose CA is trusted through a caSecretRef", func(ctx SpecContext) {
		ca := registrytest.NewCA(GinkgoT())
		reg := registrytest.NewTLSRegistry(GinkgoT(), ca)
		reg.Push(GinkgoT(), repository)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "tls-test-ca"},
			Data:       map[string][]byte{config.DefaultCASecretKey: ca.PEM},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })
		d := defaulter(map[string]config.TLSEntry{reg.Host(): {
			CASecretRef: &config.CASecretRef{Namespace: "default", Name: secret.Name, Key: config.DefaultCASecretKey},
		}})

		Expect(d.checkImageAvailabilityCached(ctx, &AlternativeImage{Reference: reg.Host() + "/" + repository}, nil)).To(Succeed())
	})

	It("finds an alternative unavailable, naming the Secret, when the CA Secret is missing", func(ctx SpecContext) {
		ca := registrytest.NewCA(GinkgoT())
		reg := registrytest.NewTLSRegistry(GinkgoT(), ca)
		reg.Push(GinkgoT(), repository)
		d := defaulter(map[string]config.TLSEntry{reg.Host(): {
			CASecretRef: &config.CASecretRef{Namespace: "default", Name: "tls-test-missing-ca", Key: config.DefaultCASecretKey},
		}})

		err := d.checkImageAvailabilityCached(ctx, &AlternativeImage{Reference: reg.Host() + "/" + repository}, nil)

		Expect(err).To(MatchError(ContainSubstring("Secret default/tls-test-missing-ca")))
	})
})
