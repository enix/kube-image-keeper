package fakeregistry

import (
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/rules"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// applyRule applies the effects of rule (which may be nil) to the
// request. It reports whether the request was fully handled, either
// answered, reset or abandoned; otherwise the caller serves it normally.
func (s *Server) applyRule(w http.ResponseWriter, r *http.Request, rule *api.Rule, p Parsed, e *api.LogEntry) bool {
	if rule == nil {
		return false
	}
	ef := rule.Effects
	for k, v := range ef.Headers {
		w.Header().Set(k, v)
	}
	if ef.LatencyMS > 0 {
		t := time.NewTimer(time.Duration(ef.LatencyMS) * time.Millisecond)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			return true
		}
	}
	if ef.Reset {
		reset(w)
		return true
	}
	// Quota is enforced by applyQuota once the request is authorized; Pass
	// needs no handling since a Status takes precedence and otherwise the
	// request is served.
	if ef.Status != 0 {
		s.writeFault(w, r, e.Host, rule.ID, ef.Status, p)
		return true
	}
	return false
}

// reset closes the connection abruptly so the client sees ECONNRESET. For
// TLS the raw connection is closed so no close_notify alert is sent.
func reset(w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if tc, ok := conn.(*tls.Conn); ok {
		conn = tc.NetConn()
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func (s *Server) writeFault(w http.ResponseWriter, r *http.Request, host, id string, status int, p Parsed) {
	code := codeUnknown
	switch status {
	case http.StatusUnauthorized:
		code = codeUnauthorized
		w.Header().Set("WWW-Authenticate", s.challenge(r, host, p))
	case http.StatusForbidden:
		code = codeDenied
	case http.StatusNotFound:
		switch p.Kind { //nolint:exhaustive // the other kinds are a plain NAME_UNKNOWN
		case api.KindManifestByTag, api.KindManifestByDigest:
			code = codeManifestUnknown
		case api.KindBlob:
			code = codeBlobUnknown
		default:
			code = codeNameUnknown
		}
	case http.StatusMethodNotAllowed:
		code = codeUnsupported
	case http.StatusTooManyRequests:
		code = codeTooManyRequests
	}
	writeError(w, r, status, code, "injected by rule "+id)
}

// evaluate finds the rule matching the request and records it in e.
func (s *Server) evaluate(host string, r *http.Request, p Parsed, e *api.LogEntry) *api.Rule {
	var rule *api.Rule
	s.store.View(func(st *state.State) {
		rule = rules.Evaluate(st, rules.Request{
			Host: host, Repo: p.Repo, Tag: p.Tag, Kind: p.Kind,
			Method: r.Method, User: e.User,
		})
	})
	if rule != nil {
		e.RuleID = rule.ID
	}
	return rule
}

// applyQuota counts the request against the quota named by rule, if any.
// It reports whether the request was answered 429. A quota name with no
// definition is ignored.
func (s *Server) applyQuota(w http.ResponseWriter, r *http.Request, rule *api.Rule, p Parsed, e *api.LogEntry) bool {
	if rule == nil || rule.Effects.Quota == "" {
		return false
	}
	var def api.QuotaDef
	var found bool
	s.store.View(func(st *state.State) {
		for _, d := range st.Quotas {
			if d.Name == rule.Effects.Quota {
				def, found = d, true
				return
			}
		}
	})
	if !found {
		return false
	}
	now := s.now()
	key := quota.Key(def, e.ClientIP, e.User)
	var remaining int
	exhausted := false
	if quota.Counts(def, p.Kind, r.Method) {
		var ok bool
		remaining, ok = s.quotas.Take(def, key, now)
		exhausted = !ok
	} else {
		used := s.quotas.Peek(def, key, now)
		remaining = max(def.Limit-used, 0)
	}
	if p.Kind == api.KindManifestByTag || p.Kind == api.KindManifestByDigest {
		w.Header().Set("RateLimit-Limit", strconv.Itoa(def.Limit)+";w="+strconv.Itoa(def.WindowSec))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining)+";w="+strconv.Itoa(def.WindowSec))
	}
	if exhausted {
		writeError(w, r, http.StatusTooManyRequests, codeTooManyRequests, "quota "+def.Name+" exhausted")
	}
	return exhausted
}
