package fakeregistry

import (
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
)

var _ = Describe("Proxy wiring", func() {
	It("forwards a label that matches a proxy pattern and serves others as synthetic", func() {
		var hits atomic.Int64
		backend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusNotFound)
		})
		srv, _, _ := newProxyServer(backend, withProxies(testerconfig.ProxyHost{Name: "mirror-*"}))
		opts := testutil.Opts(srv)

		_, err := remote.Index(testutil.Ref("mirror-a.kt.test/acme/foo:t1"), opts...)
		var terr *transport.Error
		Expect(errors.As(err, &terr)).To(BeTrue(), "err = %v", err)
		Expect(terr.StatusCode).To(Equal(http.StatusNotFound))
		forwarded := hits.Load()
		Expect(forwarded).To(BeNumerically(">=", 1), "backend requests")

		_, err = remote.Index(testutil.Ref("other.kt.test/acme/foo:t1"), opts...)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits.Load()).To(Equal(forwarded), "backend requests after the synthetic pull")
	})

	It("answers 502 when the backend sends no headers within the response-header timeout", func() {
		release := make(chan struct{})
		backend := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release })
		srv, _, _ := newProxyServer(backend, withServer(func(s *Server) {
			s.transport.ResponseHeaderTimeout = 100 * time.Millisecond
		}))
		DeferCleanup(func() { close(release) })

		start := time.Now()
		resp, body := raw(srv, http.MethodGet, mirrorFQDN, "/v2/acme/foo/manifests/t1", nil)
		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
		Expect(errorCode(body)).To(Equal(codeUnknown))
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})
})
