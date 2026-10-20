package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

// testCASource names the CA bundle of the specs, as a caSecretRef would.
const testCASource = "CA Secret kuik-system/test-ca, key ca.crt"

// badCertificateBlock is a PEM block of type CERTIFICATE whose content is no certificate.
const badCertificateBlock = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"

// testDial keeps every connection on the machine: a *.test host dials 127.0.0.1 on the same
// port, so that go-containerregistry speaks HTTPS to it as to any registry, and
// index.docker.io:443 dials *dockerHub.
func testDial(dockerHub *string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	var dialer net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.HasSuffix(strings.ToLower(host), ".test"):
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		case host == "index.docker.io" && *dockerHub != "":
			return dialer.DialContext(ctx, network, *dockerHub)
		}
		return dialer.DialContext(ctx, network, addr)
	}
}

// tlsRegistry starts a registry whose certificate ca signs for dnsName, holding app:v1, and
// returns it with its host under that name.
func tlsRegistry(ca *registrytest.CA, dnsName string) (*registrytest.Registry, string) {
	GinkgoHelper()
	reg := newRegistry(registrytest.WithTLS(ca.Certificate(dnsName)))
	reg.Push("app:v1", registrytest.Image())
	reg.Reset()
	return reg, dnsName + ":" + reg.Port()
}

func bundleOf(cas ...*registrytest.CA) TLSHost {
	var pem []byte
	for _, ca := range cas {
		pem = append(pem, ca.PEM()...)
	}
	return TLSHost{CABundle: pem, CASource: testCASource}
}

var unusable = TLSHost{CASource: testCASource, CAError: errors.New("secret not found")}

// redirectManifests sends every manifest request of reg to the same path on host.
func redirectManifests(reg *registrytest.Registry, host string) {
	reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/manifests/") {
			return false
		}
		http.Redirect(w, r, "https://"+host+r.URL.Path, http.StatusTemporaryRedirect)
		return true
	})
}

var _ = Describe("Client TLS", func() {
	var (
		client    *Client
		dockerHub string
	)

	BeforeEach(func() {
		dockerHub = ""
		client = newClient(clientOptions{dial: testDial(&dockerHub)})
	})

	check := func(host string) error {
		_, err := client.Check(ctx, Endpoint{Reference: host + "/app:v1"})
		return err
	}

	Context("with a CA bundle for a host", func() {
		It("trusts a registry whose certificate a CA of the bundle signs", func() {
			ca := registrytest.NewCA()
			_, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})

			Expect(check(host)).To(Succeed())
		})

		It("trusts every CA of a bundle holding several", func() {
			first, second := registrytest.NewCA(), registrytest.NewCA()
			_, host := tlsRegistry(second, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(first, second)})

			Expect(check(host)).To(Succeed())
		})

		It("still trusts a certificate the system roots sign", func() {
			system := registrytest.NewCA()
			client = newClient(clientOptions{
				dial:        testDial(&dockerHub),
				systemRoots: func() (*x509.CertPool, error) { return system.Pool(), nil },
			})
			_, host := tlsRegistry(system, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(registrytest.NewCA())})

			Expect(check(host)).To(Succeed())
		})

		It("still refuses a certificate neither the bundle nor the system roots sign", func() {
			client = newClient(clientOptions{
				dial:        testDial(&dockerHub),
				systemRoots: func() (*x509.CertPool, error) { return registrytest.NewCA().Pool(), nil },
			})
			_, host := tlsRegistry(registrytest.NewCA(), "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: {InsecureSkipVerify: true}})
			Expect(check(host)).To(Succeed())

			client.SetTLS(map[string]TLSHost{host: bundleOf(registrytest.NewCA())})

			Expect(checkReason(check(host))).To(Equal(kuikv1alpha1.CheckUnreachable))
		})

		It("applies the bundle to its host only, port included", func() {
			ca := registrytest.NewCA()
			_, hostA := tlsRegistry(ca, "registry.test")
			_, hostB := tlsRegistry(ca, "registry.test")
			client.SetTLS(map[string]TLSHost{hostA: bundleOf(ca)})

			Expect(check(hostA)).To(Succeed())
			Expect(checkReason(check(hostB))).To(Equal(kuikv1alpha1.CheckUnreachable))
		})

		It("applies a docker.io entry to the requests sent to index.docker.io", func() {
			ca := registrytest.NewCA()
			hub := newRegistry(registrytest.WithTLS(ca.Certificate("index.docker.io")))
			hub.Push("library/app:v1", registrytest.Image())
			dockerHub = hub.Host()
			client.SetTLS(map[string]TLSHost{"docker.io": bundleOf(ca)})

			_, err := client.Check(ctx, Endpoint{Reference: "docker.io/library/app:v1"})

			Expect(err).NotTo(HaveOccurred())
		})

		It("waits for the first read of a host's CA before sending a request, and sends it once the bundle is handed over", func() {
			ca := registrytest.NewCA()
			reg, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: PendingTLSHost(testCASource)})

			done := make(chan error, 1)
			go func() { done <- check(host) }()
			Consistently(done, "300ms").ShouldNot(Receive())
			Expect(reg.Requests("", "")).To(BeEmpty())

			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})

			Eventually(done).Should(Receive(BeNil()))
		})

		It("closes the idle connections of a host whose entry changes", func() {
			ca := registrytest.NewCA()
			reg, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})
			Expect(check(host)).To(Succeed())
			Eventually(reg.OpenConnections).Should(BeNumerically(">", 0))
			Consistently(reg.OpenConnections, "200ms").Should(BeNumerically(">", 0))

			client.SetTLS(map[string]TLSHost{host: bundleOf(ca, registrytest.NewCA())})

			Eventually(reg.OpenConnections).Should(BeZero())
		})

		It("stops trusting a CA removed from the bundle at once, open connections included", func() {
			ca := registrytest.NewCA()
			_, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})
			Expect(check(host)).To(Succeed())

			client.SetTLS(map[string]TLSHost{host: bundleOf(registrytest.NewCA())})

			Expect(checkReason(check(host))).To(Equal(kuikv1alpha1.CheckUnreachable))
		})
	})

	Context("with insecureSkipVerify for a host", func() {
		It("reaches a self-signed registry over HTTPS without verifying its certificate", func() {
			_, host := tlsRegistry(registrytest.NewCA(), "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: {InsecureSkipVerify: true}})

			Expect(check(host)).To(Succeed())
		})
	})

	Context("when a request leaves the registry's host", func() {
		It("follows the entry of the host a redirect goes to", func() {
			caA, caB := registrytest.NewCA(), registrytest.NewCA()
			regA, hostA := tlsRegistry(caA, "registry-a.test")
			_, hostB := tlsRegistry(caB, "registry-b.test")
			redirectManifests(regA, hostB)
			client.SetTLS(map[string]TLSHost{hostA: bundleOf(caA), hostB: bundleOf(caB)})

			Expect(check(hostA)).To(Succeed())
		})

		It("verifies a redirect from an insecureSkipVerify host to a host without an entry", func() {
			caB := registrytest.NewCA()
			regA, hostA := tlsRegistry(registrytest.NewCA(), "registry-a.test")
			_, hostB := tlsRegistry(caB, "registry-b.test")
			redirectManifests(regA, hostB)
			client.SetTLS(map[string]TLSHost{hostA: {InsecureSkipVerify: true}, hostB: bundleOf(caB)})
			Expect(check(hostA)).To(Succeed())

			client.SetTLS(map[string]TLSHost{hostA: {InsecureSkipVerify: true}})

			Expect(checkReason(check(hostA))).To(Equal(kuikv1alpha1.CheckUnreachable))
		})

		It("follows the entry of the token server's host", func() {
			registryCA, tokenCA := registrytest.NewCA(), registrytest.NewCA()
			tokens := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"token":"test-token"}`)
			}))
			tokens.TLS = &tls.Config{Certificates: []tls.Certificate{tokenCA.Certificate("auth.test")}}
			tokens.StartTLS()
			DeferCleanup(tokens.Close)
			_, tokenPort, _ := net.SplitHostPort(strings.TrimPrefix(tokens.URL, "https://"))
			authHost := "auth.test:" + tokenPort

			reg, host := tlsRegistry(registryCA, "registry-a.test")
			reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("Authorization") == "Bearer test-token" {
					return false
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+authHost+`/token",service="registry"`)
				w.WriteHeader(http.StatusUnauthorized)
				return true
			})
			client.SetTLS(map[string]TLSHost{host: bundleOf(registryCA), authHost: bundleOf(tokenCA)})

			Expect(check(host)).To(Succeed())
		})

		It("follows the entry of a redirect whose host is written in uppercase", func() {
			caA, caB := registrytest.NewCA(), registrytest.NewCA()
			regA, hostA := tlsRegistry(caA, "registry-a.test")
			_, hostB := tlsRegistry(caB, "registry-b.test")
			redirectManifests(regA, strings.ToUpper(hostB))
			client.SetTLS(map[string]TLSHost{hostA: bundleOf(caA), hostB: bundleOf(caB)})

			Expect(check(hostA)).To(Succeed())
		})
	})

	Context("when the CA bundle of a host is unusable", func() {
		It("reports Unreachable, naming the CA source, without sending any request", func() {
			reg, host := tlsRegistry(registrytest.NewCA(), "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: unusable})

			err := check(host)

			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))
			Expect(err.Error()).To(ContainSubstring(testCASource))
			Expect(reg.Requests("", "")).To(BeEmpty())
		})

		DescribeTable("reports Unreachable, naming the CA source, when the bundle",
			func(bundle func(ca *registrytest.CA) []byte) {
				ca := registrytest.NewCA()
				reg, host := tlsRegistry(ca, "registry-a.test")
				client.SetTLS(map[string]TLSHost{host: {CABundle: bundle(ca), CASource: testCASource}})

				err := check(host)

				Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))
				Expect(err.Error()).To(ContainSubstring(testCASource))
				Expect(reg.Requests("", "")).To(BeEmpty())
			},
			Entry("holds no PEM certificate", func(*registrytest.CA) []byte {
				return []byte("not a certificate")
			}),
			Entry("holds a block that does not parse as a certificate", func(ca *registrytest.CA) []byte {
				return append(append([]byte{}, ca.PEM()...), badCertificateBlock...)
			}),
			Entry("ends with a truncated block", func(ca *registrytest.CA) []byte {
				return append(append([]byte{}, ca.PEM()...), "-----BEGIN CERTIFICATE-----\nMIIB\n"...)
			}),
			Entry("holds a block that is not a certificate", func(ca *registrytest.CA) []byte {
				return append(append([]byte{}, ca.PEM()...), "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"...)
			}),
		)

		DescribeTable("accepts a bundle with text outside its blocks",
			func(bundle func(ca *registrytest.CA) []byte) {
				ca := registrytest.NewCA()
				_, host := tlsRegistry(ca, "registry-a.test")
				client.SetTLS(map[string]TLSHost{host: {CABundle: bundle(ca), CASource: testCASource}})

				Expect(check(host)).To(Succeed())
			},
			Entry("before them", func(ca *registrytest.CA) []byte {
				return append([]byte("# corporate CA\n"), ca.PEM()...)
			}),
			Entry("after them", func(ca *registrytest.CA) []byte {
				return append(append([]byte{}, ca.PEM()...), "# end of bundle\n"...)
			}),
		)

		It("does not count the request it refused in kuik_registry_requests_total", func() {
			_, host := tlsRegistry(registrytest.NewCA(), "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: unusable})

			Expect(check(host)).NotTo(Succeed())

			Expect(countedFor(host)).To(BeZero())
		})

		It("leaves the other hosts reachable", func() {
			ca := registrytest.NewCA()
			_, hostA := tlsRegistry(ca, "registry-a.test")
			_, hostB := tlsRegistry(ca, "registry-b.test")
			client.SetTLS(map[string]TLSHost{hostA: unusable, hostB: bundleOf(ca)})

			Expect(check(hostA)).NotTo(Succeed())
			Expect(check(hostB)).To(Succeed())
		})
	})

	Context("on each operation", func() {
		It("applies the entries of the source and of the destination to a copy", func() {
			sourceCA, destinationCA := registrytest.NewCA(), registrytest.NewCA()
			_, source := tlsRegistry(sourceCA, "source.test")
			dst, destination := tlsRegistry(destinationCA, "destination.test")
			client.SetTLS(map[string]TLSHost{source: bundleOf(sourceCA), destination: bundleOf(destinationCA)})

			_, err := client.Copy(ctx, Endpoint{Reference: source + "/app:v1"},
				Endpoint{Reference: destination + "/mirror/app"}, []string{"v1"})

			Expect(err).NotTo(HaveOccurred())
			_, err = dst.Head("mirror/app:v1")
			Expect(err).NotTo(HaveOccurred())
		})

		It("applies the host's entry to tag listings and deletions", func() {
			ca := registrytest.NewCA()
			reg, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})

			tags, err := client.ListTags(ctx, Endpoint{Reference: host + "/app"})
			Expect(err).NotTo(HaveOccurred())
			Expect(tags).To(ContainElement("v1"))

			Expect(client.DeleteTag(ctx, Endpoint{Reference: host + "/app:v1"})).To(Succeed())
			_, err = reg.Head("app:v1")
			Expect(err).To(HaveOccurred())
		})

		It("reports SourceUnreachable when the source blobs redirect to a host whose CA is unusable, counting the manifest read alone", func() {
			ca := registrytest.NewCA()
			src, source := tlsRegistry(ca, "source.test")
			_, blobs := tlsRegistry(ca, "blobs.test")
			_, destination := tlsRegistry(ca, "destination.test")
			src.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.Contains(r.URL.Path, "/blobs/") {
					return false
				}
				http.Redirect(w, r, "https://"+blobs+r.URL.Path, http.StatusTemporaryRedirect)
				return true
			})
			client.SetTLS(map[string]TLSHost{source: bundleOf(ca), blobs: unusable, destination: bundleOf(ca)})

			_, err := client.Copy(ctx, Endpoint{Reference: source + "/app:v1"},
				Endpoint{Reference: destination + "/mirror/app"}, []string{"v1"})

			copyErr, ok := errors.AsType[*CopyError](err)
			Expect(ok).To(BeTrue(), "a *CopyError, got %v", err)
			Expect(copyErr.Reason).To(Equal(kuikv1alpha1.CopySourceUnreachable))
			Expect(counted(source, "Copy", resultOk)).To(Equal(1.0))
			Expect(countedFor(source)).To(Equal(1.0))
		})

		DescribeTable("reports the side of a copy whose CA is unusable",
			func(sourceUnusable bool) {
				ca := registrytest.NewCA()
				_, source := tlsRegistry(ca, "source.test")
				_, destination := tlsRegistry(ca, "destination.test")
				settings := map[string]TLSHost{source: bundleOf(ca), destination: bundleOf(ca)}
				want := kuikv1alpha1.CopyDestinationUnreachable
				if sourceUnusable {
					settings[source], want = unusable, kuikv1alpha1.CopySourceUnreachable
				} else {
					settings[destination] = unusable
				}
				client.SetTLS(settings)

				_, err := client.Copy(ctx, Endpoint{Reference: source + "/app:v1"},
					Endpoint{Reference: destination + "/mirror/app"}, []string{"v1"})

				copyErr, ok := errors.AsType[*CopyError](err)
				Expect(ok).To(BeTrue(), "a *CopyError, got %v", err)
				Expect(copyErr.Reason).To(Equal(want))
				Expect(err.Error()).To(ContainSubstring(testCASource))
				if sourceUnusable {
					Expect(countedFor(source)).To(BeZero())
				}
			},
			Entry("SourceUnreachable for the source", true),
			Entry("DestinationUnreachable for the destination", false),
		)
	})

	Context("with a host that has a tls entry", func() {
		It("never reaches it over plain HTTP from a secure endpoint", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			reg.Reset()
			client.SetTLS(map[string]TLSHost{reg.Host(): {InsecureSkipVerify: true}})

			Expect(checkReason(check(reg.Host()))).To(Equal(kuikv1alpha1.CheckUnreachable))
			Expect(reg.Requests("", "")).To(BeEmpty())
		})

		It("counts once a sent HTTPS request that failed, even when its plain HTTP fallback is refused", func() {
			ca := registrytest.NewCA()
			reg := newRegistry(registrytest.WithTLS(ca.Certificate()))
			reg.Push("app:v1", registrytest.Image())
			client.SetTLS(map[string]TLSHost{reg.Host(): bundleOf(registrytest.NewCA())})

			Expect(checkReason(check(reg.Host()))).To(Equal(kuikv1alpha1.CheckUnreachable))

			Expect(counted(reg.Host(), "Check", string(kuikv1alpha1.CheckUnreachable))).To(Equal(1.0))
		})

		// A guard: it passes before the TLS settings exist, and pins that refusing plain HTTP
		// to a host with an entry spares the CR entries declared insecure.
		It("reaches it over plain HTTP from an insecure endpoint, whatever the entry holds", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			client.SetTLS(map[string]TLSHost{reg.Host(): unusable})

			_, err := client.Check(ctx, Endpoint{Reference: reg.Host() + "/app:v1", Insecure: true})

			Expect(err).NotTo(HaveOccurred())
		})

		It("still applies the entry over HTTPS to a secure endpoint on the same host as an insecure one", func() {
			ca := registrytest.NewCA()
			_, host := tlsRegistry(ca, "registry-a.test")
			client.SetTLS(map[string]TLSHost{host: bundleOf(ca)})

			By("ignoring the entry for the insecure endpoint")
			_, err := client.Check(ctx, Endpoint{Reference: host + "/app:v1", Insecure: true})
			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))

			By("applying it to the secure one")
			Expect(check(host)).To(Succeed())
		})
	})
	Context("through an HTTPS proxy", func() {
		leaf := func(certificate tls.Certificate) []*x509.Certificate {
			GinkgoHelper()
			parsed, err := x509.ParseCertificate(certificate.Certificate[0])
			Expect(err).NotTo(HaveOccurred())
			return []*x509.Certificate{parsed}
		}

		DescribeTable("verifies the proxy hop against the system roots, whatever the entry holds",
			func(settings func(ca *registrytest.CA) TLSHost) {
				system, entryCA := registrytest.NewCA(), registrytest.NewCA()
				verify := connectionVerifier("registry-a.test:5000", settings(entryCA),
					func() (*x509.CertPool, error) { return system.Pool(), nil })

				By("applying the entry to the registry hop")
				Expect(verify(tls.ConnectionState{ServerName: "registry-a.test",
					PeerCertificates: leaf(entryCA.Certificate("registry-a.test"))})).To(Succeed())

				By("verifying the proxy hop as if the host had no entry")
				Expect(verify(tls.ConnectionState{ServerName: "proxy.test",
					PeerCertificates: leaf(entryCA.Certificate("proxy.test"))})).NotTo(Succeed())
				Expect(verify(tls.ConnectionState{ServerName: "proxy.test",
					PeerCertificates: leaf(system.Certificate("proxy.test"))})).To(Succeed())
			},
			Entry("with a CA bundle", func(ca *registrytest.CA) TLSHost { return bundleOf(ca) }),
			Entry("with insecureSkipVerify", func(*registrytest.CA) TLSHost { return TLSHost{InsecureSkipVerify: true} }),
		)

		It("refuses an HTTPS proxy addressed by IP in front of a registry addressed by IP", func() {
			proxied := func(proxy string) func(*http.Request) (*url.URL, error) {
				return refuseIPProxy(func(*http.Request) (*url.URL, error) { return url.Parse(proxy) })
			}
			request := func(target string) *http.Request {
				req, err := http.NewRequest(http.MethodGet, target, nil)
				Expect(err).NotTo(HaveOccurred())
				return req
			}

			_, err := proxied("https://192.0.2.10:3128")(request("https://127.0.0.1:5000/v2/"))
			Expect(err).To(HaveOccurred())

			By("letting through a proxy that can be told apart from the registry")
			Expect(proxied("https://proxy.test:3128")(request("https://127.0.0.1:5000/v2/"))).
				To(HaveField("Host", "proxy.test:3128"))
			Expect(proxied("http://192.0.2.10:3128")(request("https://127.0.0.1:5000/v2/"))).
				To(HaveField("Host", "192.0.2.10:3128"))
			Expect(proxied("https://192.0.2.10:3128")(request("https://registry-a.test:5000/v2/"))).
				To(HaveField("Host", "192.0.2.10:3128"))
		})

		It("refuses an HTTPS proxy that shares the registry's host name", func() {
			proxied := func(proxy string) func(*http.Request) (*url.URL, error) {
				return refuseIPProxy(func(*http.Request) (*url.URL, error) { return url.Parse(proxy) })
			}
			request := func(target string) *http.Request {
				req, err := http.NewRequest(http.MethodGet, target, nil)
				Expect(err).NotTo(HaveOccurred())
				return req
			}

			_, err := proxied("https://REGISTRY-A.test:8443")(request("https://registry-a.test:5000/v2/"))
			Expect(err).To(MatchError(ContainSubstring("registry-a.test")))

			By("comparing the names in the IDNA form net/http sends")
			_, err = proxied("https://bücher.test:8443")(request("https://xn--bcher-kva.test:5000/v2/"))
			Expect(err).To(HaveOccurred())
			_, err = proxied("https://xn--bcher-kva.test:8443")(request("https://bücher.test:5000/v2/"))
			Expect(err).To(HaveOccurred())

			By("refusing a proxy name IDNA cannot convert")
			_, err = proxied("https://a\u00adb\u200d.test:8443")(request("https://registry-a.test:5000/v2/"))
			Expect(err).To(HaveOccurred())

			By("letting through a plain HTTP proxy of that name, and an HTTPS proxy of another name")
			Expect(proxied("http://registry-a.test:8080")(request("https://registry-a.test:5000/v2/"))).
				To(HaveField("Host", "registry-a.test:8080"))
			Expect(proxied("https://proxy.test:8443")(request("https://registry-a.test:5000/v2/"))).
				To(HaveField("Host", "proxy.test:8443"))
		})
	})
})
