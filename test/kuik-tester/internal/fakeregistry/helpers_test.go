package fakeregistry

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Names the tests share.
const (
	baseDomain   = "kt.test"
	plainHost    = "http" // label of the plain-HTTP listener
	upstream     = "upstream"
	upstreamFQDN = upstream + "." + baseDomain
	mirror       = "mirror"
	mirrorFQDN   = mirror + "." + baseDomain
	bear         = "bear"
	bearFQDN     = bear + "." + baseDomain
	private      = "private"
	privateFQDN  = private + "." + baseDomain

	repo       = "acme/foo"
	teamRepo   = "team/**"
	alice      = "alice"
	loopback   = "127.0.0.0/8"
	manifest1  = "/v2/acme/foo/manifests/t1"
	tagCluster = "t1_cluster-a"

	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	headerContentLength = "Content-Length"
	headerRemaining     = "RateLimit-Remaining"
	headerChallenge     = "WWW-Authenticate"
	headerForwardedFor  = "X-Forwarded-For"
	headerForwardProto  = "X-Forwarded-Proto"
	basicChallenge      = `Basic realm="kuik-tester"`
	bearChallenge       = `Bearer realm="http://bear.kt.test/token",service="bear.kt.test"`
)

// testCfg returns the configuration newServer starts from.
func testCfg() *testerconfig.Config {
	return &testerconfig.Config{BaseDomain: baseDomain, PlainHTTPHost: plainHost, LogSize: 100}
}

type serverOpts struct {
	cfg   *testerconfig.Config
	quota *quota.Counters
	tls   bool
	h2    bool
	file  string
	setup []func(*state.State)
	hooks []func(*Server)
}

type serverOption func(*serverOpts)

// withConfig edits the configuration, testCfg() to begin with.
func withConfig(f func(*testerconfig.Config)) serverOption {
	return func(o *serverOpts) { f(o.cfg) }
}

// withQuotas makes the server count quotas in q.
func withQuotas(q *quota.Counters) serverOption {
	return func(o *serverOpts) { o.quota = q }
}

// withTLS serves over TLS, with HTTP/2 when h2 is set.
func withTLS(h2 bool) serverOption {
	return func(o *serverOpts) { o.tls, o.h2 = true, h2 }
}

// withStateFile keeps the state in the file at path instead of in memory.
func withStateFile(path string) serverOption {
	return func(o *serverOpts) { o.file = path }
}

// withState edits the state before the first request.
func withState(f func(*state.State)) serverOption {
	return func(o *serverOpts) { o.setup = append(o.setup, f) }
}

// fakeClock is a clock the specs advance by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// withClock makes the server read time from c.
func withClock(c *fakeClock) serverOption {
	return withServer(func(s *Server) { s.now = c.Now })
}

// withServer edits the Server after New, before the listener starts.
func withServer(f func(*Server)) serverOption {
	return func(o *serverOpts) { o.hooks = append(o.hooks, f) }
}

// liveTokens returns how many bearer tokens s holds.
func liveTokens(s *Server) int {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	return len(s.tokens)
}

// serveDirect serves r through a fresh Server on an empty store, after
// setup, and returns the recorder and the single log entry.
func serveDirect(cfg *testerconfig.Config, setup func(*state.State), r *http.Request) (*httptest.ResponseRecorder, api.LogEntry) {
	GinkgoHelper()
	st, err := state.Open("")
	Expect(err).NotTo(HaveOccurred())
	if setup != nil {
		setState(st, setup)
	}
	log := reqlog.New(10)
	w := httptest.NewRecorder()
	Expect(cfg.Resolve()).To(Succeed())
	proxies, err := dest.NewProxies(cfg)
	Expect(err).NotTo(HaveOccurred())
	New(cfg, proxies, st, log, quota.New()).Handler().ServeHTTP(w, r)
	es := log.Query(reqlog.Filter{})
	Expect(es).To(HaveLen(1), "log entries")
	return w, es[0]
}

// newServer starts a registry server on testCfg() and an empty state.
func newServer(opts ...serverOption) (*httptest.Server, *state.Store, *reqlog.Log) {
	GinkgoHelper()
	o := serverOpts{cfg: testCfg(), quota: quota.New()}
	for _, f := range opts {
		f(&o)
	}
	st, err := state.Open(o.file)
	Expect(err).NotTo(HaveOccurred())
	log := reqlog.New(o.cfg.LogSize)
	Expect(o.cfg.Resolve()).To(Succeed())
	proxies, err := dest.NewProxies(o.cfg)
	Expect(err).NotTo(HaveOccurred())
	s := New(o.cfg, proxies, st, log, o.quota)
	for _, f := range o.hooks {
		f(s)
	}
	srv := httptest.NewUnstartedServer(s.Handler())
	if o.tls {
		srv.EnableHTTP2 = o.h2
		srv.StartTLS()
	} else {
		srv.Start()
	}
	DeferCleanup(srv.Close)
	for _, f := range o.setup {
		setState(st, f)
	}
	return srv, st, log
}

// setState applies fn to the state.
func setState(st *state.Store, fn func(*state.State)) {
	GinkgoHelper()
	Expect(st.Update(func(s *state.State) error { fn(s); return nil })).To(Succeed())
}

// addRule adds an enabled rule.
func addRule(st *state.Store, r api.Rule) {
	GinkgoHelper()
	r.Enabled = true
	setState(st, func(s *state.State) { s.Rules = append(s.Rules, r) })
}

// authState has users alice and bob and a grant letting only alice pull
// team/** on the private host.
func authState(s *state.State) {
	s.Users = []api.User{{Name: alice, Password: "pw"}, {Name: "bob", Password: "pw"}}
	s.Grants = []api.Grant{{
		ID: "g", Host: private, Repo: teamRepo,
		Actions: []api.Action{api.ActionPull}, Users: []string{alice},
	}}
}

// bearerState makes bear a bearer host where only alice may pull team/**.
func bearerState(s *state.State) {
	s.Users = []api.User{{Name: alice, Password: "pw"}}
	s.HostAuth = []api.HostAuth{{Host: bear, Scheme: api.SchemeBearer}}
	s.Grants = []api.Grant{{
		ID: "g", Host: bear, Repo: teamRepo,
		Actions: []api.Action{api.ActionPull}, Users: []string{alice},
	}}
}

// trustLoopback makes the test client a trusted proxy.
func trustLoopback(c *testerconfig.Config) { c.TrustedProxies = []string{loopback} }

// waitTimeout bounds how long a spec waits for a log entry to appear.
const waitTimeout = 3 * time.Second

func waitEntry(log *reqlog.Log, f func(api.LogEntry) bool) api.LogEntry {
	GinkgoHelper()
	var found api.LogEntry
	Eventually(func() bool {
		for _, e := range log.Query(reqlog.Filter{}) {
			if f(e) {
				found = e
				return true
			}
		}
		return false
	}).WithTimeout(waitTimeout).Should(BeTrue(), "no matching log entry")
	return found
}

// waitEntries waits until the log holds exactly n entries and returns them.
func waitEntries(log *reqlog.Log, n int) []api.LogEntry {
	GinkgoHelper()
	var es []api.LogEntry
	Eventually(func() int {
		es = log.Query(reqlog.Filter{})
		return len(es)
	}).WithTimeout(waitTimeout).Should(Equal(n), "log entries")
	return es
}

// errorCode returns the first errors[].code of an OCI error body.
func errorCode(body []byte) string {
	GinkgoHelper()
	var eb struct{ Errors []struct{ Code string } }
	Expect(json.Unmarshal(body, &eb)).To(Succeed(), "body = %s", body)
	Expect(eb.Errors).To(HaveLen(1), "body = %s", body)
	return eb.Errors[0].Code
}

func hasEntry(log *reqlog.Log, kind api.Kind, method string) api.LogEntry {
	GinkgoHelper()
	return waitEntry(log, func(e api.LogEntry) bool { return e.Kind == kind && (method == "" || e.Method == method) })
}

// raw sends a request with the given Host header straight to srv.
func raw(srv *httptest.Server, method, host, path string, hdr map[string]string) (*http.Response, []byte) {
	GinkgoHelper()
	return rawBody(srv, method, host, path, hdr, "")
}

// rawForm sends a form-encoded POST with the given Host header to srv.
func rawForm(srv *httptest.Server, host, path, form string) (*http.Response, []byte) {
	GinkgoHelper()
	return rawBody(srv, http.MethodPost, host, path, map[string]string{headerContentType: "application/x-www-form-urlencoded"}, form)
}

func rawBody(srv *httptest.Server, method, host, path string, hdr map[string]string, body string) (*http.Response, []byte) {
	GinkgoHelper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// wantChallenge checks that resp is a 401 carrying the WWW-Authenticate
// value want.
func wantChallenge(resp *http.Response, want string) {
	GinkgoHelper()
	Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized), "challenge %q, want %q", resp.Header.Get("WWW-Authenticate"), want)
	Expect(resp.Header.Get("WWW-Authenticate")).To(Equal(want), "status %d", resp.StatusCode)
}

func basic(user, pw string) map[string]string {
	return map[string]string{headerAuthorization: "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pw))}
}

func bearerHdr(tok string) map[string]string {
	return map[string]string{headerAuthorization: "Bearer " + tok}
}

func authOpts(srv *httptest.Server, user, pw string) []remote.Option {
	return append(testutil.Opts(srv), remote.WithAuth(&authn.Basic{Username: user, Password: pw}))
}

func indexDigest(srv *httptest.Server, ref string) v1.Hash {
	GinkgoHelper()
	idx, err := remote.Index(testutil.Ref(ref), testutil.Opts(srv)...)
	Expect(err).NotTo(HaveOccurred(), "pull %s", ref)
	d, _ := idx.Digest()
	return d
}

func statusOf(err error) int {
	if te, ok := errors.AsType[*transport.Error](err); ok {
		return te.StatusCode
	}
	return 0
}
