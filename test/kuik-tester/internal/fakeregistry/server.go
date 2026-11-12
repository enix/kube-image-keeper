// Package fakeregistry serves the public registry endpoint: synthetic hosts and
// proxy hosts.
package fakeregistry

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/ref"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Server is the public registry endpoint.
type Server struct {
	cfg         *testerconfig.Config
	proxies     *dest.Proxies
	store       *state.Store
	log         *reqlog.Log
	trustedNets []*net.IPNet

	quotas *quota.Counters

	transport *http.Transport

	tokMu  sync.Mutex
	tokens map[string]tokenInfo
	// nextSweep is when expired tokens are next dropped; guarded by tokMu.
	nextSweep time.Time
	maxTokens int

	now func() time.Time
}

// New returns a Server for cfg backed by st that records requests in log
// and counts quotas in q. cfg comes from testerconfig.Load or has been through
// Resolve. proxies must be built from cfg by dest.NewProxies.
func New(cfg *testerconfig.Config, proxies *dest.Proxies, st *state.Store, log *reqlog.Log, q *quota.Counters) *Server {
	return &Server{
		cfg: cfg, proxies: proxies, store: st, log: log, quotas: q, trustedNets: cfg.TrustedNets,
		transport: dest.NewTransport(), tokens: map[string]tokenInfo{},
		maxTokens: defaultMaxTokens, now: time.Now,
	}
}

// Handler serves every <label>.<baseDomain> host.
func (s *Server) Handler() http.Handler {
	return s.logged(func(w http.ResponseWriter, r *http.Request, e *api.LogEntry) {
		label, ok := s.hostLabel(r.Host)
		if !ok {
			e.Host = r.Host
			e.UnknownHost = true
			writeError(w, r, http.StatusNotFound, codeNameUnknown, "unknown host "+r.Host)
			return
		}
		e.Host = label
		s.serve(w, r, label, e)
	})
}

// FixedHostHandler serves every request as host label, whatever its Host
// header, except a Host of <label>-<suffix>.<baseDomain>[:port], served under
// that owned label. It backs the plain-HTTP listener.
func (s *Server) FixedHostHandler(label string) http.Handler {
	return s.logged(func(w http.ResponseWriter, r *http.Request, e *api.LogEntry) {
		served := label
		if l, ok := s.hostLabel(r.Host); ok && strings.HasPrefix(l, label+"-") {
			served = l
		}
		e.Host = served
		s.serve(w, r, served, e)
	})
}

func (s *Server) hostLabel(host string) (string, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	label, ok := strings.CutSuffix(host, "."+s.cfg.BaseDomain)
	if !ok || !ref.ValidLabel(label) {
		return "", false
	}
	return label, true
}

// proxy returns the backend of the proxy that serves a host label.
func (s *Server) proxy(label string) (*url.URL, bool) {
	p, ok := s.proxies.Lookup(label)
	if !ok {
		return nil, false
	}
	return p.Backend, true
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, host string, e *api.LogEntry) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	p, ok := Classify(r.Method, r.URL.Path)
	if !ok {
		writeError(w, r, http.StatusNotFound, codeNameUnknown, "unknown endpoint "+r.URL.Path)
		return
	}
	e.Kind, e.Repo, e.Reference = p.Kind, p.Repo, p.Tag
	if p.Tag == "" {
		e.Reference = p.Digest
	}
	presented, ok := s.identify(w, r, host, p, e)
	if !ok {
		return
	}
	rule := s.evaluate(host, r, p, e)
	if s.applyRule(w, r, rule, p, e) {
		return
	}
	if !s.authorize(w, r, host, p, e, presented) {
		return
	}
	if s.applyQuota(w, r, rule, p, e) {
		return
	}
	backend, isProxy := s.proxy(host)
	if p.Kind == api.KindToken {
		if s.bearer(host) {
			s.serveToken(w, r, host, p, e)
			return
		}
		writeError(w, r, http.StatusNotFound, codeNameUnknown, "unknown endpoint "+r.URL.Path)
		return
	}
	if isProxy {
		s.forward(w, r, backend, e)
		return
	}
	s.serveSynthetic(w, r, host, p)
}
