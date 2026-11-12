package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// testReloader returns a reloader on a fresh pair of serial 1.
func testReloader() *certReloader {
	GinkgoHelper()
	certFile, keyFile := writePair(GinkgoT().TempDir(), 1)
	certs, err := newCertReloader(certFile, keyFile)
	Expect(err).NotTo(HaveOccurred())
	return certs
}

func registryServer(h http.Handler, certs *certReloader) *http.Server {
	return newRegistryServer(ctx, "", h, certs)
}

func defaultServer(h http.Handler, certs *certReloader) *http.Server {
	return &http.Server{
		Handler:   h,
		TLSConfig: &tls.Config{GetCertificate: certs.getCertificate, MinVersion: tls.VersionTLS12},
	}
}

var _ = Describe("newRegistryServer", func() {
	DescribeTable("negotiates the HTTP version a TLS client gets",
		func(build func(h http.Handler, certs *certReloader) *http.Server, want string) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			srv := build(h, testReloader())
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				_ = srv.ServeTLS(ln, "", "")
			}()
			DeferCleanup(srv.Close)
			tr := &http.Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test server
				ForceAttemptHTTP2: true,
			}
			DeferCleanup(tr.CloseIdleConnections)
			resp, err := (&http.Client{Transport: tr}).Get("https://" + ln.Addr().String())
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.Proto).To(Equal(want))
		},
		Entry("the registry server stays on HTTP/1.1", registryServer, "HTTP/1.1"),
		Entry("a default server would speak HTTP/2 (control)", defaultServer, "HTTP/2.0"),
	)
})

var _ = Describe("the listener servers", func() {
	DescribeTable("gives every listener the same limits and base context",
		func(build func(base context.Context) *http.Server) {
			type key struct{}
			base := context.WithValue(ctx, key{}, "base")
			srv := build(base)
			Expect(srv.ReadHeaderTimeout).To(Equal(10 * time.Second))
			Expect(srv.IdleTimeout).To(Equal(120 * time.Second))
			Expect(srv.MaxHeaderBytes).To(Equal(64 << 10))
			Expect(srv.ReadTimeout).To(BeZero())
			Expect(srv.WriteTimeout).To(BeZero())
			Expect(srv.BaseContext(nil)).To(BeIdenticalTo(base))
		},
		Entry("the admin and plain HTTP server", func(base context.Context) *http.Server {
			return newServer(base, "", http.NotFoundHandler())
		}),
		Entry("the registry server without TLS", func(base context.Context) *http.Server {
			return newRegistryServer(base, "", http.NotFoundHandler(), nil)
		}),
		Entry("the registry server with TLS", func(base context.Context) *http.Server {
			return newRegistryServer(base, "", http.NotFoundHandler(), testReloader())
		}),
	)

	// net/http adds 4 KiB of slack to MaxHeaderBytes.
	DescribeTable("answers a request header block by size",
		func(size, want int) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			srv := newServer(ctx, "", h)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				_ = srv.Serve(ln)
			}()
			DeferCleanup(srv.Close)
			req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String(), nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("X-Pad", strings.Repeat("a", size))
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(want))
		},
		Entry("a 32 KiB header block is served", 32<<10, http.StatusOK),
		Entry("an 80 KiB header block is refused", 80<<10, http.StatusRequestHeaderFieldsTooLarge),
	)

	// An open stream (SSE) or a latency fault must not hold up the shutdown:
	// cancelling the base context ends the request contexts.
	DescribeTable("ends an open request when the base context is cancelled",
		func(build func(base context.Context, h http.Handler) *http.Server) {
			base, cancelBase := context.WithCancel(ctx)
			started := make(chan struct{})
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			})
			srv := build(base, h)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				_ = srv.Serve(ln)
			}()
			resp, err := http.Get("http://" + ln.Addr().String())
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			<-started
			cancelBase()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			Expect(srv.Shutdown(ctx)).To(Succeed())
		},
		Entry("the admin and plain HTTP server", func(base context.Context, h http.Handler) *http.Server {
			return newServer(base, "", h)
		}),
		Entry("the registry server", func(base context.Context, h http.Handler) *http.Server {
			return newRegistryServer(base, "", h, nil)
		}),
	)
})
