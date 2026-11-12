package fakeregistry

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	slowRule  = "slow"
	resetRule = "rst"
)

var _ = Describe("Fault rules", func() {
	It("answers an injected status", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: "r", Effects: api.Effects{Status: 503}})
		_, err := remote.Head(testutil.Ref("upstream.kt.test/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(503), "err = %v", err)
	})

	It("adds a rate-limit header to a 200", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: "r", Effects: api.Effects{Headers: map[string]string{headerRemaining: "0;w=21600"}}})
		resp, _ := raw(srv, http.MethodHead, upstreamFQDN, manifest1, nil)
		Expect(resp.StatusCode).To(Equal(200), "headers %v", resp.Header)
		Expect(resp.Header.Get(headerRemaining)).To(Equal("0;w=21600"), "headers %v", resp.Header)
		Expect(resp.Header.Get("Docker-Content-Digest")).NotTo(BeEmpty(), "headers %v", resp.Header)
	})

	It("delays the answer by the latency", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: "r", Effects: api.Effects{LatencyMS: 100}})
		start := time.Now()
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, "/v2/", nil)
		Expect(resp.StatusCode).To(Equal(200))
		Expect(time.Since(start)).To(BeNumerically(">=", 100*time.Millisecond))
	})

	It("logs a request whose client gave up during the latency with status 0", func() {
		srv, st, log := newServer()
		addRule(st, api.Rule{ID: slowRule, Effects: api.Effects{LatencyMS: 5000}})
		c := &http.Client{Timeout: 50 * time.Millisecond}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v2/", nil)
		req.Host = upstreamFQDN
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		Expect(err).To(HaveOccurred(), "expected timeout")
		e := waitEntry(log, func(e api.LogEntry) bool { return e.RuleID == slowRule })
		Expect(e.Status).To(BeZero())
	})

	It("resets the connection", func() {
		srv, st, log := newServer()
		addRule(st, api.Rule{ID: resetRule, Effects: api.Effects{Reset: true}})
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, "GET /v2/ HTTP/1.1\r\nHost: upstream.kt.test\r\n\r\n")
		line, err := bufio.NewReader(conn).ReadString('\n')
		Expect(err).To(HaveOccurred(), "got a response: %q", line)
		Expect(err).To(Or(MatchError(io.EOF), MatchError(syscall.ECONNRESET)))
		e := waitEntry(log, func(e api.LogEntry) bool { return e.RuleID == resetRule })
		Expect(e.Status).To(BeZero())
	})

	It("exempts a request matched by a pass rule from lower rules", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: "pass", Target: api.Target{Repo: repo, Tag: "t1"}, Effects: api.Effects{Pass: true}})
		addRule(st, api.Rule{ID: "fail", Target: api.Target{Repo: "acme/*"}, Effects: api.Effects{Status: 503}})
		_, err := remote.Head(testutil.Ref("upstream.kt.test/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred(), "t1")
		_, err = remote.Head(testutil.Ref("upstream.kt.test/acme/foo:t2"), testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(503), "t2: %v", err)
	})

	It("sends the same headers but no body for a faulted HEAD", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: "r", Effects: api.Effects{Status: 404}})
		head, hb := raw(srv, http.MethodHead, upstreamFQDN, manifest1, nil)
		get, gb := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		Expect(head.StatusCode).To(Equal(404), "HEAD")
		Expect(get.StatusCode).To(Equal(404), "GET")
		for _, h := range []string{headerContentType, headerContentLength} {
			Expect(head.Header.Get(h)).To(Equal(get.Header.Get(h)), h)
			Expect(get.Header.Get(h)).NotTo(BeEmpty(), h)
		}
		Expect(hb).To(BeEmpty(), "HEAD body")
		Expect(bytes.Contains(gb, []byte("MANIFEST_UNKNOWN"))).To(BeTrue(), "GET body %q", gb)
	})

	It("logs the request with its caller, digest and the faulting rule", func() {
		srv, st, log := newServer()
		resp, _ := raw(srv, http.MethodGet, upstreamFQDN, manifest1, map[string]string{"User-Agent": "go-containerregistry/v0.22.1"})
		want := resp.Header.Get("Docker-Content-Digest")
		es := waitEntries(log, 1)
		e := es[0]
		Expect(want).NotTo(BeEmpty())
		Expect(e.Host).To(Equal(upstream), "entry = %+v", e)
		Expect(e.Kind).To(Equal(api.KindManifestByTag), "entry = %+v", e)
		Expect(e.Reference).To(Equal("t1"), "entry = %+v", e)
		Expect(e.Repo).To(Equal(repo), "entry = %+v", e)
		Expect(e.Status).To(Equal(200), "entry = %+v", e)
		Expect(e.Digest).To(Equal(want), "entry = %+v", e)
		Expect(e.Caller).To(Equal("kuik"), "entry = %+v", e)
		Expect(e.ClientIP).To(Equal("127.0.0.1"), "entry = %+v", e)
		Expect(e.Method).To(Equal(http.MethodGet), "entry = %+v", e)
		addRule(st, api.Rule{ID: "f", Effects: api.Effects{Status: 503}})
		raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		e = waitEntry(log, func(e api.LogEntry) bool { return e.RuleID == "f" })
		Expect(e.Status).To(Equal(503), "faulted entry = %+v", e)
	})

	It("logs rejected requests with no kind and a 404", func() {
		srv, _, log := newServer()
		raw(srv, http.MethodGet, "evil.example.com", "/v2/", nil)
		raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/nonsense", nil)
		es := waitEntries(log, 2)
		var evil, nonsense *api.LogEntry
		for i := range es {
			switch es[i].Host {
			case "evil.example.com":
				evil = &es[i]
			case upstream:
				nonsense = &es[i]
			}
		}
		Expect(evil).NotTo(BeNil())
		Expect(evil.Kind).To(BeEmpty(), "evil = %+v", evil)
		Expect(evil.Status).To(Equal(404), "evil = %+v", evil)
		Expect(nonsense).NotTo(BeNil())
		Expect(nonsense.Kind).To(BeEmpty(), "nonsense = %+v", nonsense)
		Expect(nonsense.Status).To(Equal(404), "nonsense = %+v", nonsense)
	})

	// A reset over HTTP/1.1 + TLS must surface as a connection reset, not as a
	// clean EOF preceded by a TLS close_notify.
	It("makes a reset over HTTP/1.1 and TLS an abrupt connection reset", func() {
		srv, st, log := newServer(withTLS(false))
		addRule(st, api.Rule{ID: resetRule, Effects: api.Effects{Reset: true}})
		conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, "GET /v2/ HTTP/1.1\r\nHost: upstream.kt.test\r\n\r\n")
		_, err = bufio.NewReader(conn).ReadString('\n')
		Expect(err).To(MatchError(syscall.ECONNRESET))
		e := waitEntry(log, func(e api.LogEntry) bool { return e.RuleID == resetRule })
		Expect(e.Status).To(BeZero())
	})

	// Over HTTP/2 the connection cannot be hijacked: the request must still
	// fail and be logged without a status.
	It("fails and logs without a status a reset over HTTP/2", func() {
		srv, st, log := newServer(withTLS(true))
		addRule(st, api.Rule{ID: resetRule, Effects: api.Effects{Reset: true}})
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Host = upstreamFQDN
		var proto string
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(i httptrace.GotConnInfo) {
				if c, ok := i.Conn.(*tls.Conn); ok {
					proto = c.ConnectionState().NegotiatedProtocol
				}
			},
		}))
		resp, err := srv.Client().Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		Expect(err).To(HaveOccurred(), "expected an error")
		Expect(proto).To(Equal("h2"), "negotiated protocol (the test would not cover ErrAbortHandler)")
		e := waitEntry(log, func(e api.LogEntry) bool { return e.RuleID == resetRule })
		Expect(e.Status).To(BeZero())
	})

	DescribeTable("maps an injected status to its OCI error code",
		func(status int, path, wantCode string, challenge bool) {
			srv, st, _ := newServer()
			addRule(st, api.Rule{ID: "r", Effects: api.Effects{Status: status}})
			get, gb := raw(srv, http.MethodGet, upstreamFQDN, path, nil)
			head, hb := raw(srv, http.MethodHead, upstreamFQDN, path, nil)
			Expect(get.StatusCode).To(Equal(status), "GET")
			Expect(head.StatusCode).To(Equal(status), "HEAD")
			Expect(errorCode(gb)).To(Equal(wantCode))
			if challenge {
				Expect(get.Header.Get(headerChallenge)).NotTo(BeEmpty(), "GET headers %v", get.Header)
			} else {
				Expect(get.Header.Get(headerChallenge)).To(BeEmpty(), "GET headers %v", get.Header)
			}
			Expect(hb).To(BeEmpty(), "HEAD body")
			for _, h := range []string{headerContentType, headerContentLength} {
				Expect(head.Header.Get(h)).To(Equal(get.Header.Get(h)), h)
			}
		},
		Entry("maps 401 to UNAUTHORIZED with a challenge", 401, manifest1, "UNAUTHORIZED", true),
		Entry("maps 403 to DENIED", 403, manifest1, "DENIED", false),
		Entry("maps 404 on a manifest by tag to MANIFEST_UNKNOWN", 404, manifest1, "MANIFEST_UNKNOWN", false),
		Entry("maps 404 on a manifest by digest to MANIFEST_UNKNOWN", 404, "/v2/acme/foo/manifests/sha256:"+strings.Repeat("a", 64), "MANIFEST_UNKNOWN", false),
		Entry("maps 404 on a blob to BLOB_UNKNOWN", 404, "/v2/acme/foo/blobs/sha256:"+strings.Repeat("a", 64), "BLOB_UNKNOWN", false),
		Entry("maps 404 on a tags list to NAME_UNKNOWN", 404, "/v2/acme/foo/tags/list", "NAME_UNKNOWN", false),
		Entry("maps 404 on the catalog to NAME_UNKNOWN", 404, "/v2/_catalog", "NAME_UNKNOWN", false),
		Entry("maps 405 to UNSUPPORTED", 405, manifest1, "UNSUPPORTED", false),
		Entry("maps 429 to TOOMANYREQUESTS", 429, manifest1, "TOOMANYREQUESTS", false),
		Entry("maps 500 to UNKNOWN", 500, manifest1, "UNKNOWN", false),
		Entry("maps 503 to UNKNOWN", 503, manifest1, "UNKNOWN", false),
	)

	DescribeTable("combines the effects of one rule",
		func(ef api.Effects, want int, minDelay time.Duration) {
			srv, st, _ := newServer()
			ef.Headers = map[string]string{headerRemaining: "0;w=60"}
			addRule(st, api.Rule{ID: "r", Effects: ef})
			start := time.Now()
			resp, _ := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
			Expect(time.Since(start)).To(BeNumerically(">=", minDelay))
			Expect(resp.StatusCode).To(Equal(want))
			Expect(resp.Header.Get(headerRemaining)).To(Equal("0;w=60"), "headers %v", resp.Header)
		},
		Entry("adds the headers to an injected 503", api.Effects{Status: 503}, 503, time.Duration(0)),
		Entry("adds the headers to an injected 429", api.Effects{Status: 429}, 429, time.Duration(0)),
		Entry("delays then adds the headers to an injected 503", api.Effects{LatencyMS: 100, Status: 503}, 503, 100*time.Millisecond),
		Entry("adds the headers to a passed request", api.Effects{Pass: true}, 200, time.Duration(0)),
		Entry("delays then adds the headers to a passed request", api.Effects{LatencyMS: 100, Pass: true}, 200, 100*time.Millisecond),
	)

	It("resets after the latency and sends nothing", func() {
		srv, st, _ := newServer()
		addRule(st, api.Rule{ID: resetRule, Effects: api.Effects{
			Headers: map[string]string{headerRemaining: "0;w=60"}, LatencyMS: 100, Reset: true}})
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		start := time.Now()
		_, _ = io.WriteString(conn, "GET /v2/ HTTP/1.1\r\nHost: upstream.kt.test\r\n\r\n")
		n, err := conn.Read(make([]byte, 512))
		Expect(err).To(Or(MatchError(io.EOF), MatchError(syscall.ECONNRESET)))
		Expect(n).To(BeZero(), "bytes read")
		Expect(time.Since(start)).To(BeNumerically(">=", 100*time.Millisecond))
	})

	DescribeTable("keeps a fault on acme/foo:t1 to that target",
		func(host, path string, want int) {
			srv, st, _ := newServer()
			addRule(st, api.Rule{ID: "r", Target: api.Target{Host: upstream, Repo: repo, Tag: "t1"}, Effects: api.Effects{Status: 503}})
			resp, _ := raw(srv, http.MethodHead, host, path, nil)
			Expect(resp.StatusCode).To(Equal(want))
		},
		Entry("faults the targeted tag", upstreamFQDN, "/v2/acme/foo/manifests/t1", 503),
		Entry("spares a repo sharing the prefix", upstreamFQDN, "/v2/acme/foo-bar/manifests/t1", 200),
		Entry("spares a nested repo", upstreamFQDN, "/v2/acme/foo/bar/manifests/t1", 200),
		Entry("spares another tag", upstreamFQDN, "/v2/acme/foo/manifests/t2", 200),
		Entry("spares another host", "alt1.kt.test", "/v2/acme/foo/manifests/t1", 200),
	)

	It("does not fault a request by the digest of the faulted tag", func() {
		srv, st, _ := newServer()
		d := indexDigest(srv, upstreamFQDN+"/acme/foo:t1")
		addRule(st, api.Rule{ID: "r", Target: api.Target{Host: upstream, Repo: repo, Tag: "t1"}, Effects: api.Effects{Status: 503}})
		resp, _ := raw(srv, http.MethodHead, upstreamFQDN, "/v2/acme/foo/manifests/"+d.String(), nil)
		Expect(resp.StatusCode).To(Equal(200), "by digest")
		resp, _ = raw(srv, http.MethodHead, upstreamFQDN, manifest1, nil)
		Expect(resp.StatusCode).To(Equal(503), "by tag")
	})
})
