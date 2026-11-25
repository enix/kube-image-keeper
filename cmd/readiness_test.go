package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var _ = Describe("The webhook server readiness check", func() {
	var (
		server webhook.Server
		ready  func() error
	)

	BeforeEach(func() {
		certDir := GinkgoT().TempDir()
		writeSelfSignedCertificate(certDir)
		server = webhook.NewServer(webhook.Options{
			Host:    "127.0.0.1",
			Port:    freePort(),
			CertDir: certDir,
		})
		check := webhookServerReady(server)
		ready = func() error { return check(nil) }
	})

	It("is not ready before the webhook server listens", func() {
		Expect(ready()).NotTo(Succeed())
	})

	It("is ready once the webhook server accepts connections", func() {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		DeferCleanup(func() {
			cancel()
			Eventually(stopped).Should(BeClosed())
		})
		go func() {
			defer GinkgoRecover()
			defer close(stopped)
			Expect(server.Start(ctx)).To(Succeed())
		}()

		Eventually(ready).Should(Succeed())
	})
})

// freePort returns a loopback port nothing listens on
func freePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(listener.Close()).To(Succeed()) }()
	return listener.Addr().(*net.TCPAddr).Port
}

// writeSelfSignedCertificate writes the tls.crt and tls.key the webhook server loads from dir
func writeSelfSignedCertificate(dir string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	Expect(err).NotTo(HaveOccurred())

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	Expect(os.WriteFile(filepath.Join(dir, "tls.crt"), certPEM, 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "tls.key"), keyPEM, 0o600)).To(Succeed())
}
