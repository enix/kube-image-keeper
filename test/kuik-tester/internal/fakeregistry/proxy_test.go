package fakeregistry

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// withProxies replaces the proxies of the configuration. An empty Backend
// stands for the backend of newProxyServer.
func withProxies(proxies ...testerconfig.ProxyHost) serverOption {
	return withConfig(func(c *testerconfig.Config) { c.Proxies = proxies })
}

// newProxyServer starts backend (ggcr's in-memory registry when nil) and a
// server with the proxy mirror in front of it, unless opts set other
// proxies; those with an empty Backend point at backend too.
func newProxyServer(backend http.Handler, opts ...serverOption) (*httptest.Server, *state.Store, *reqlog.Log) {
	GinkgoHelper()
	if backend == nil {
		backend = testutil.NewRegistry()
	}
	b := httptest.NewServer(backend)
	DeferCleanup(b.Close)
	all := make([]serverOption, 0, len(opts)+2)
	all = append(all, withProxies(testerconfig.ProxyHost{Name: mirror}))
	all = append(all, opts...)
	all = append(all, withConfig(func(c *testerconfig.Config) {
		for i := range c.Proxies {
			if c.Proxies[i].Backend == "" {
				c.Proxies[i].Backend = b.URL
			}
		}
	}))
	return newServer(all...)
}

const (
	noDelete = "nodelete"
	manage   = "manage"
)

// recordingBackend answers 404 and keeps the headers of the last request.
type recordingBackend struct {
	mu  sync.Mutex
	hdr http.Header
}

func (b *recordingBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.hdr = r.Header.Clone()
	b.mu.Unlock()
	w.WriteHeader(http.StatusNotFound)
}

func (b *recordingBackend) seen() http.Header {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hdr
}

// pushPullDelete pushes a synthetic index to the mirror host, pulls it back,
// lists the tags and deletes it, then checks the log shows each step forwarded.
// The in-process and the zot specs share it.
func pushPullDelete(srv *httptest.Server, log *reqlog.Log, repo string) {
	GinkgoHelper()
	opts := testutil.Opts(srv)
	ref := testutil.Ref(mirrorFQDN + "/" + repo + ":" + tagCluster)
	idx, err := synth.Build(repo, 1, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(remote.WriteIndex(ref, idx, opts...)).To(Succeed())
	want, _ := idx.Digest()
	Expect(indexDigest(srv, ref.String())).To(Equal(want), "digest")
	tags, err := remote.List(ref.Context(), opts...)
	Expect(err).NotTo(HaveOccurred())
	Expect(tags).To(Equal([]string{tagCluster}))
	Expect(remote.Delete(ref, opts...)).To(Succeed())
	for _, k := range []api.Kind{api.KindUpload, api.KindManifestPut, api.KindTagsList, api.KindDelete} {
		Expect(hasEntry(log, k, "").Forwarded).To(BeTrue(), "%s not forwarded", k)
	}
	e := hasEntry(log, api.KindManifestByTag, http.MethodGet)
	Expect(e.Digest).To(Equal(want.String()), "entry %+v", e)
	Expect(e.Forwarded).To(BeTrue(), "entry %+v", e)
}

var _ = Describe("Proxy mirror", func() {
	It("forwards a push, a pull, a tag list and a delete", func() {
		srv, _, log := newProxyServer(nil)
		pushPullDelete(srv, log, repo)
	})

	It("answers 405 to a delete on a host whose rule forbids it, without forwarding", func() {
		srv, st, log := newProxyServer(nil, withProxies(testerconfig.ProxyHost{Name: mirror}, testerconfig.ProxyHost{Name: noDelete}))
		addRule(st, api.Rule{ID: "nd",
			Target:  api.Target{Host: noDelete, Kinds: []api.Kind{api.KindDelete}},
			Effects: api.Effects{Status: 405}})
		opts := testutil.Opts(srv)
		idx, _ := synth.Build(repo, 1, nil)
		ref := testutil.Ref("nodelete.kt.test/acme/foo:t1")
		Expect(remote.WriteIndex(ref, idx, opts...)).To(Succeed())
		err := remote.Delete(ref, opts...)
		Expect(statusOf(err)).To(Equal(405), "err = %v", err)
		_, err = remote.Head(testutil.Ref("mirror.kt.test/acme/foo:t1"), opts...)
		Expect(err).NotTo(HaveOccurred())
		e := hasEntry(log, api.KindDelete, "")
		Expect(e.Forwarded).To(BeFalse(), "entry %+v", e)
		Expect(e.Status).To(Equal(405), "entry %+v", e)
	})

	It("requires a grant to push and lets anonymous pull", func() {
		srv, st, _ := newProxyServer(nil)
		setState(st, func(s *state.State) {
			s.Users = []api.User{{Name: manage, Password: "pw"}}
			s.Grants = []api.Grant{{ID: "g", Host: mirror, Repo: "**",
				Actions: []api.Action{api.ActionPush, api.ActionDelete}, Users: []string{manage}}}
		})
		idx, _ := synth.Build(repo, 1, nil)
		ref := testutil.Ref("mirror.kt.test/acme/foo:t1")
		err := remote.WriteIndex(ref, idx, testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(401), "anonymous push: %v", err)
		Expect(remote.WriteIndex(ref, idx, authOpts(srv, manage, "pw")...)).To(Succeed())
		_, err = remote.Index(ref, testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred(), "anonymous pull")
	})

	It("does not forward Authorization and sets X-Forwarded-* by trust", func() {
		be := &recordingBackend{}
		srv, st, _ := newProxyServer(be)
		setState(st, func(s *state.State) { s.Users = []api.User{{Name: manage, Password: "pw"}} })
		raw(srv, http.MethodGet, mirrorFQDN, manifest1, basic(manage, "pw"))
		h := be.seen()
		Expect(h).NotTo(BeNil(), "backend saw nothing")
		Expect(h.Get(headerAuthorization)).To(BeEmpty(), "backend saw %v", h)
		Expect(h.Get(headerForwardProto)).To(Equal(schemeHTTP), "backend saw %v", h)
		Expect(h.Get(headerForwardedFor)).To(Equal("127.0.0.1"), "untrusted: backend X-Forwarded-For")

		be = &recordingBackend{}
		srv, st, _ = newProxyServer(be, withConfig(trustLoopback))
		setState(st, func(s *state.State) { s.Users = []api.User{{Name: manage, Password: "pw"}} })
		hdr := basic(manage, "pw")
		hdr[headerForwardProto] = schemeHTTPS
		hdr[headerForwardedFor] = "203.0.113.7"
		raw(srv, http.MethodGet, mirrorFQDN, manifest1, hdr)
		h = be.seen()
		Expect(h).NotTo(BeNil(), "backend saw nothing")
		Expect(h.Get(headerAuthorization)).To(BeEmpty(), "backend saw %v", h)
		Expect(h.Get(headerForwardProto)).To(Equal(schemeHTTPS), "backend saw %v", h)
		Expect(h.Get(headerForwardedFor)).To(Equal("203.0.113.7"), "trusted: backend X-Forwarded-For")
	})

	It("rewrites the Location header of the backend", func() {
		var loc atomic.Value
		loc.Store("")
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := loc.Load().(string)
			if l == "abs" {
				l = "http://" + r.Host + "/v2/x/blobs/uploads/u?state=s"
			}
			w.Header().Set("Location", l)
			w.WriteHeader(http.StatusAccepted)
		})
		srv, _, _ := newProxyServer(be, withConfig(trustLoopback))
		post := func(hdr map[string]string) string {
			GinkgoHelper()
			resp, _ := raw(srv, http.MethodPost, mirrorFQDN, "/v2/x/blobs/uploads/", hdr)
			return resp.Header.Get("Location")
		}
		loc.Store("abs")
		Expect(post(nil)).To(Equal("http://mirror.kt.test/v2/x/blobs/uploads/u?state=s"))
		Expect(post(map[string]string{headerForwardProto: schemeHTTPS})).To(Equal("https://mirror.kt.test/v2/x/blobs/uploads/u?state=s"))
		loc.Store("/v2/x/blobs/uploads/u")
		Expect(post(nil)).To(Equal("/v2/x/blobs/uploads/u"))
	})

	It("relays the final response after an early hint and applies header rules", func() {
		digest := "sha256:" + strings.Repeat("a", 64)
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", "</x>; rel=preload")
			w.WriteHeader(http.StatusEarlyHints)
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusOK)
		})
		srv, st, log := newProxyServer(be)
		addRule(st, api.Rule{ID: "h", Target: api.Target{Host: mirror},
			Effects: api.Effects{Headers: map[string]string{"X-Test": "1"}}})
		resp, _ := raw(srv, http.MethodGet, mirrorFQDN, manifest1, nil)
		Expect(resp.StatusCode).To(Equal(200))
		Expect(resp.Header.Values("X-Test")).To(Equal([]string{"1"}))
		Expect(resp.Header.Values("Docker-Distribution-API-Version")).To(HaveLen(1), "API version")
		e := hasEntry(log, api.KindManifestByTag, http.MethodGet)
		Expect(e.Status).To(Equal(200), "entry status")
		Expect(e.Digest).To(Equal(digest), "entry digest")
	})

	It("applies latency to a forwarded request and answers a status rule without forwarding", func() {
		var hits atomic.Int32
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		})
		srv, st, log := newProxyServer(be)
		addRule(st, api.Rule{ID: slowRule, Target: api.Target{Kinds: []api.Kind{api.KindManifestByTag}},
			Effects: api.Effects{LatencyMS: 100, Pass: true}})
		start := time.Now()
		resp, _ := raw(srv, http.MethodGet, mirrorFQDN, manifest1, nil)
		d := time.Since(start)
		Expect(resp.StatusCode).To(Equal(200), "after %s", d)
		Expect(d).To(BeNumerically(">=", 100*time.Millisecond))
		Expect(hits.Load()).To(BeEquivalentTo(1))

		setState(st, func(s *state.State) { s.Rules = nil })
		addRule(st, api.Rule{ID: "down", Target: api.Target{Kinds: []api.Kind{api.KindTagsList}},
			Effects: api.Effects{Status: 503}})
		resp, _ = raw(srv, http.MethodGet, mirrorFQDN, "/v2/acme/foo/tags/list", nil)
		Expect(resp.StatusCode).To(Equal(503))
		Expect(hits.Load()).To(BeEquivalentTo(1))
		Expect(hasEntry(log, api.KindTagsList, "").Forwarded).To(BeFalse(), "forwarded despite the status rule")
	})

	It("answers 502 in registry format when the backend is down", func() {
		dead := httptest.NewServer(http.NotFoundHandler())
		dead.Close()
		srv, _, log := newProxyServer(nil, withProxies(testerconfig.ProxyHost{Name: mirror, Backend: dead.URL}))
		resp, body := raw(srv, http.MethodGet, mirrorFQDN, manifest1, nil)
		var eb struct{ Errors []struct{ Code string } }
		Expect(json.Unmarshal(body, &eb)).To(Succeed(), "body %s", body)
		Expect(resp.StatusCode).To(Equal(502), "body %s", body)
		Expect(eb.Errors).To(HaveLen(1), "body %s", body)
		Expect(eb.Errors[0].Code).To(Equal("UNKNOWN"))
		Expect(resp.Header.Get("Docker-Distribution-API-Version")).To(Equal("registry/2.0"), "API version header lost on 502")
		e := hasEntry(log, api.KindManifestByTag, "")
		Expect(e.Forwarded).To(BeTrue(), "entry %+v", e)
		Expect(e.Status).To(Equal(502), "entry %+v", e)
	})

	It("logs a client that cancels while the backend is answering with status 0", func() {
		release := make(chan struct{})
		started := make(chan struct{}, 1)
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-release
		})
		srv, _, log := newProxyServer(be)
		defer close(release)
		ctx, cancel := context.WithCancel(ctx)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+manifest1, nil)
		req.Host = mirrorFQDN
		done := make(chan struct{})
		go func() {
			defer close(done)
			if resp, err := srv.Client().Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		<-started
		cancel()
		<-done
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindManifestByTag })
		Expect(e.Forwarded).To(BeTrue(), "entry %+v", e)
		Expect(e.Status).To(BeZero(), "entry %+v", e)
	})

	It("does not forward token requests", func() {
		var hits atomic.Int32
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
		srv, st, _ := newProxyServer(be)
		resp, _ := raw(srv, http.MethodGet, mirrorFQDN, "/token", nil)
		Expect(resp.StatusCode).To(Equal(404), "basic host")
		Expect(hits.Load()).To(BeZero(), "basic host")
		setState(st, func(s *state.State) { s.HostAuth = []api.HostAuth{{Host: mirror, Scheme: api.SchemeBearer}} })
		resp, body := raw(srv, http.MethodGet, mirrorFQDN, "/token", nil)
		Expect(resp.StatusCode).To(Equal(200), "bearer host: body %s", body)
		Expect(string(body)).To(ContainSubstring(`"token"`), "bearer host")
		Expect(hits.Load()).To(BeZero(), "bearer host")
	})

	It("matches the host case-insensitively and ignores the port", func() {
		var hits atomic.Int32
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
		srv, _, _ := newProxyServer(be)
		raw(srv, http.MethodGet, "MIRROR.kt.test:443", "/v2/", nil)
		Expect(hits.Load()).To(BeEquivalentTo(1))
	})

	It("does not forward an unknown endpoint", func() {
		var hits atomic.Int32
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
		srv, _, _ := newProxyServer(be)
		resp, _ := raw(srv, http.MethodGet, mirrorFQDN, "/v2/acme/foo/referrers/sha256:"+strings.Repeat("a", 64), nil)
		Expect(resp.StatusCode).To(Equal(404))
		Expect(hits.Load()).To(BeZero())
	})

	// A client that goes away in the middle of an upload body is logged with
	// status 0, not as a backend failure.
	It("logs a client that disappears during an upload body with status 0", func() {
		got := make(chan struct{}, 1)
		be := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 5)
			if _, err := io.ReadFull(r.Body, buf); err == nil {
				got <- struct{}{}
			}
			_, _ = io.Copy(io.Discard, r.Body) // blocks until the proxy gives up
		})
		srv, _, log := newProxyServer(be)
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		// 100 bytes announced, 5 sent, then the client disappears.
		_, err = io.WriteString(conn, "PATCH /v2/acme/foo/blobs/uploads/abc HTTP/1.1\r\nHost: mirror.kt.test\r\nContent-Length: 100\r\nContent-Type: application/octet-stream\r\n\r\nhello")
		Expect(err).NotTo(HaveOccurred())
		Eventually(got).WithTimeout(5*time.Second).Should(Receive(), "the backend never received the first bytes")
		_ = conn.Close()
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Kind == api.KindUpload })
		Expect(e.Forwarded).To(BeTrue(), "entry %+v, want forwarded with status 0", e)
		Expect(e.Status).To(BeZero(), "entry %+v, want forwarded with status 0", e)
	})
})
