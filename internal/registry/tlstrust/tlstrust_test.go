package tlstrust

import (
	"context"
	"maps"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	host        = "harbor.corp.tld"
	changedHost = "changed.tld"
)

// recorder is a Target that keeps the last settings it was handed.
type recorder struct {
	mu   sync.Mutex
	last map[string]registry.TLSHost
}

func (r *recorder) SetTLS(hosts map[string]registry.TLSHost) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = maps.Clone(hosts)
}

func (r *recorder) settings() map[string]registry.TLSHost {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.last)
}

func (r *recorder) host(name string) registry.TLSHost {
	return r.settings()[name]
}

func withTLS(tls config.TLS) *config.Config {
	return &config.Config{TLS: tls}
}

func caRef(name, key string) config.TLSEntry {
	return config.TLSEntry{CASecretRef: &config.CASecretRef{Name: name, Key: key}}
}

func newNamespace() string {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "kuik-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), ns) })
	return ns.Name
}

func createSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	GinkgoHelper()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, Data: data}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	return secret
}

// secretCache starts a cache of the Secrets of namespaces for the spec.
func secretCache(namespaces ...string) cache.Cache {
	GinkgoHelper()
	watched := map[string]cache.Config{}
	for _, ns := range namespaces {
		watched[ns] = cache.Config{}
	}
	c, err := cache.New(cfg, cache.Options{Scheme: scheme.Scheme, DefaultNamespaces: watched})
	Expect(err).NotTo(HaveOccurred())
	specCtx, stop := context.WithCancel(ctx)
	DeferCleanup(stop)
	go func() {
		defer GinkgoRecover()
		Expect(c.Start(specCtx)).To(Succeed())
	}()
	return c
}

// start runs trust for the spec and waits until it is ready.
func start(trust *Trust) {
	GinkgoHelper()
	specCtx, stop := context.WithCancel(ctx)
	DeferCleanup(stop)
	go func() {
		defer GinkgoRecover()
		Expect(trust.Start(specCtx)).To(Succeed())
	}()
	Eventually(func() error { return trust.Ready(nil) }).Should(Succeed())
}

var _ = Describe("Trust", func() {
	var (
		namespace string
		ca        []byte
		clients   []*recorder
		client    *recorder
	)

	BeforeEach(func() {
		namespace = newNamespace()
		ca = registrytest.NewCA().PEM()
		clients = []*recorder{{}, {}}
		client = clients[0]
	})

	newTrust := func(cfg *config.Config) *Trust {
		return New(secretCache(namespace), namespace, cfg, clients[0], clients[1])
	}

	Context("with a caSecretRef entry", func() {
		It("hands every client the bundle the Secret holds under the caSecretRef key", func() {
			createSecret(namespace, "harbor-ca", map[string][]byte{"bundle.pem": ca})
			start(newTrust(withTLS(config.TLS{host: caRef("harbor-ca", "bundle.pem")})))

			want := registry.TLSHost{CABundle: ca, CASource: "CA Secret " + namespace + "/harbor-ca, key bundle.pem"}
			for _, c := range clients {
				Expect(c.host(host)).To(Equal(want), "the settings are handed before the process is ready")
			}
		})

		DescribeTable("marks the host's CA unusable, naming the Secret",
			func(secret, cause string) {
				secrets := secretCache(namespace)
				switch secret {
				case "wrong key":
					createSecret(namespace, "harbor-ca", map[string][]byte{"tls.crt": ca})
				case "empty key":
					createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: {}})
				case "not PEM":
					createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: []byte("not a certificate")})
				case "other namespace":
					// The cache sees both namespaces: only the cluster resource namespace counts.
					other := newNamespace()
					secrets = secretCache(namespace, other)
					createSecret(other, "harbor-ca", map[string][]byte{config.DefaultCAKey: ca})
				}
				start(New(secrets, namespace, withTLS(config.TLS{host: caRef("harbor-ca", config.DefaultCAKey)}), clients[0], clients[1]))

				Expect(client.host(host).CAError).To(MatchError(ContainSubstring(cause)))
				Expect(client.host(host).Pending).To(BeFalse(), "the Secret was read")
				Expect(client.host(host).CABundle).To(BeNil())
				Expect(client.host(host).CASource).To(Equal("CA Secret " + namespace + "/harbor-ca, key ca.crt"))
			},
			Entry("when the Secret does not exist", "missing", "not found"),
			Entry("when the Secret has no such key", "wrong key", "no key ca.crt"),
			Entry("when the Secret's key is empty", "empty key", "PEM certificates"),
			Entry("when the Secret's key holds no PEM certificate", "not PEM", "PEM certificates"),
			Entry("when a Secret of that name exists only in another namespace", "other namespace", "not found"),
		)

		It("holds the host's requests until its Secret has been read once", func() {
			createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: ca})
			trust := newTrust(withTLS(config.TLS{host: caRef("harbor-ca", config.DefaultCAKey)}))

			Expect(client.settings()).To(HaveKeyWithValue(host,
				registry.PendingTLSHost("CA Secret "+namespace+"/harbor-ca, key ca.crt")))
			Expect(trust.Ready(nil)).NotTo(Succeed())

			start(trust)
			Eventually(func() []byte { return client.host(host).CABundle }).Should(Equal(ca))
		})
	})

	Context("with an entry that sets neither field", func() {
		It("hands no entry for its host", func() {
			start(newTrust(withTLS(config.TLS{host: {}, changedHost: {InsecureSkipVerify: true}})))

			Expect(client.settings()).To(Equal(map[string]registry.TLSHost{changedHost: {InsecureSkipVerify: true}}))
		})
	})

	Context("with an insecureSkipVerify entry", func() {
		It("hands every client the host without verification", func() {
			start(newTrust(withTLS(config.TLS{host: {InsecureSkipVerify: true}})))

			for _, c := range clients {
				Eventually(func() registry.TLSHost { return c.host(host) }).
					Should(Equal(registry.TLSHost{InsecureSkipVerify: true}))
			}
		})
	})

	Context("without a restart", func() {
		It("applies a change of the CA Secret's data", func() {
			secret := createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: ca})
			start(newTrust(withTLS(config.TLS{host: caRef("harbor-ca", config.DefaultCAKey)})))
			Eventually(func() []byte { return client.host(host).CABundle }).Should(Equal(ca))

			rotated := registrytest.NewCA().PEM()
			secret.Data[config.DefaultCAKey] = rotated
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())

			Eventually(func() []byte { return client.host(host).CABundle }).Should(Equal(rotated))
		})

		It("applies a CA Secret created after the start", func() {
			start(newTrust(withTLS(config.TLS{host: caRef("harbor-ca", config.DefaultCAKey)})))
			Expect(client.host(host).CAError).To(MatchError(ContainSubstring("not found")))

			createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: ca})

			Eventually(func() []byte { return client.host(host).CABundle }).Should(Equal(ca))
			Expect(client.host(host).CAError).NotTo(HaveOccurred())
		})

		DescribeTable("marks the host's CA unusable",
			func(change, cause string) {
				secret := createSecret(namespace, "harbor-ca", map[string][]byte{config.DefaultCAKey: ca})
				start(newTrust(withTLS(config.TLS{host: caRef("harbor-ca", config.DefaultCAKey)})))
				Eventually(func() []byte { return client.host(host).CABundle }).Should(Equal(ca))

				if change == "delete" {
					Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
				} else {
					secret.Data = map[string][]byte{"tls.crt": ca}
					Expect(k8sClient.Update(ctx, secret)).To(Succeed())
				}

				Eventually(func() error { return client.host(host).CAError }).Should(MatchError(ContainSubstring(cause)))
				Expect(client.host(host).CABundle).To(BeNil())
			},
			Entry("once its Secret is deleted", "delete", "not found"),
			Entry("once the key is removed from its Secret", "remove key", "no key ca.crt"),
		)

		It("applies a reloaded config that adds, changes or removes an entry", func() {
			createSecret(namespace, "lab-ca", map[string][]byte{config.DefaultCAKey: ca})
			trust := newTrust(withTLS(config.TLS{
				"removed.tld": {InsecureSkipVerify: true},
				changedHost:   caRef("lab-ca", config.DefaultCAKey),
			}))
			start(trust)
			Eventually(func() []byte { return client.host(changedHost).CABundle }).Should(Equal(ca))

			trust.SetConfig(withTLS(config.TLS{
				changedHost: {InsecureSkipVerify: true},
				"added.tld": caRef("lab-ca", config.DefaultCAKey),
			}))

			Eventually(client.settings).Should(Equal(map[string]registry.TLSHost{
				changedHost: {InsecureSkipVerify: true},
				"added.tld": {CABundle: ca, CASource: "CA Secret " + namespace + "/lab-ca, key ca.crt"},
			}))
		})
	})
})
