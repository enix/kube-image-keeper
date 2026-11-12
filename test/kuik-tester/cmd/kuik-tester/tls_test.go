package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// writePair writes (over any previous files) the PEM certificate and key of a
// self-signed pair for 127.0.0.1 with the given serial number.
func writePair(dir string, serial int64) (certFile, keyFile string) {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "kuik-tester test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDER, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	Expect(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)).To(Succeed())
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	Expect(os.WriteFile(keyFile, keyPEM, 0o600)).To(Succeed())
	return certFile, keyFile
}

// bumps counts the calls to bump, so that each one sets a modification time
// later than every earlier one, whatever the filesystem's granularity.
var bumps atomic.Int64

// bump sets the modification time of each file to a point in time that is
// later than the one of every previous bump, so a change is never hidden by
// coarse file timestamps.
func bump(files ...string) {
	GinkgoHelper()
	t := time.Now().Add(time.Duration(bumps.Add(1)) * time.Minute)
	for _, f := range files {
		Expect(os.Chtimes(f, t, t)).To(Succeed())
	}
}

func servedSerial(certs *certReloader) int64 {
	GinkgoHelper()
	cert, err := certs.getCertificate(nil)
	Expect(err).NotTo(HaveOccurred())
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	Expect(err).NotTo(HaveOccurred())
	return leaf.SerialNumber.Int64()
}

var _ = Describe("certReloader", func() {
	var dir, certFile, keyFile string
	var certs *certReloader

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		certFile, keyFile = writePair(dir, 1)
		var err error
		certs, err = newCertReloader(certFile, keyFile)
		Expect(err).NotTo(HaveOccurred())
	})

	It("serves the pair it loaded", func() {
		Expect(servedSerial(certs)).To(BeEquivalentTo(1))
	})

	It("loads the files again after they change", func() {
		writePair(dir, 2)
		bump(certFile, keyFile)
		Expect(servedSerial(certs)).To(BeEquivalentTo(2))
	})

	It("keeps the previous pair when the new files do not load", func() {
		Expect(os.WriteFile(certFile, []byte("garbage"), 0o600)).To(Succeed())
		bump(certFile, keyFile)
		Expect(servedSerial(certs)).To(BeEquivalentTo(1))

		writePair(dir, 3)
		bump(certFile, keyFile)
		Expect(servedSerial(certs)).To(BeEquivalentTo(3))
	})

	It("fails at start when the files do not load", func() {
		missing := filepath.Join(dir, "absent.key")
		_, err := newCertReloader(certFile, missing)
		Expect(err).To(MatchError(ContainSubstring(missing)))
	})

	It("hands a renewed certificate to the next TLS client", func() {
		srv := newRegistryServer(ctx, "", http.NotFoundHandler(), certs)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		go func() {
			defer GinkgoRecover()
			_ = srv.ServeTLS(ln, "", "")
		}()
		DeferCleanup(srv.Close)
		peerSerial := func() int64 {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test server
			}
			defer tr.CloseIdleConnections()
			resp, err := (&http.Client{Transport: tr}).Get("https://" + ln.Addr().String())
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			return resp.TLS.PeerCertificates[0].SerialNumber.Int64()
		}
		Expect(peerSerial()).To(BeEquivalentTo(1))
		writePair(dir, 2)
		bump(certFile, keyFile)
		Expect(peerSerial()).To(BeEquivalentTo(2))
	})

	It("completes every concurrent handshake while the pair rotates", func() {
		srv := newRegistryServer(ctx, "", http.NotFoundHandler(), certs)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		go func() {
			defer GinkgoRecover()
			_ = srv.ServeTLS(ln, "", "")
		}()
		DeferCleanup(srv.Close)
		addr := ln.Addr().String()
		peerSerial := func() int64 {
			GinkgoHelper()
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			cfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed test server
			conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = conn.Close() }()
			return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
		}

		// Each goroutine completes a first handshake before the pair rotates,
		// so the rotations overlap with handshakes in flight.
		var wg, started sync.WaitGroup
		for range 8 {
			wg.Add(1)
			started.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				var once sync.Once
				defer once.Do(started.Done) // never leave the spec goroutine waiting if the first handshake fails
				Expect(peerSerial()).To(BeEquivalentTo(1))
				once.Do(started.Done)
				for range 25 {
					Expect(peerSerial()).To(And(BeNumerically(">=", 1), BeNumerically("<=", 6)))
				}
			}()
		}
		started.Wait()
		for n := int64(2); n <= 6; n++ {
			writePair(dir, n)
			bump(certFile, keyFile)
		}
		wg.Wait()
		Expect(peerSerial()).To(BeEquivalentTo(6))
	})
})
