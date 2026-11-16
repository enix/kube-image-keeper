package fakeregistry

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/rules"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// identify resolves the caller's identity into e.User. presented reports
// whether the caller sent valid credentials: a valid Bearer token (an
// anonymous one included) or valid Basic. ok is false after answering 401
// for credentials that are present but invalid.
func (s *Server) identify(w http.ResponseWriter, r *http.Request, host string, p Parsed, e *api.LogEntry) (presented, ok bool) {
	scheme, tok, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if strings.EqualFold(scheme, "Bearer") {
		user, valid := s.lookupToken(strings.TrimSpace(tok), s.now())
		if !valid {
			e.User = "(invalid token)"
			s.unauthorized(w, r, host, p, "invalid or expired token")
			return false, false
		}
		e.User = user
		return true, true
	}
	if !strings.EqualFold(scheme, "Basic") {
		return false, true
	}
	name, pw, hasBasic := r.BasicAuth()
	if !hasBasic || !s.checkPassword(name, pw) {
		e.User = strings.TrimSpace(name + " (invalid)")
		s.unauthorized(w, r, host, p, "authentication required")
		return false, false
	}
	e.User = name
	return true, true
}

// authorize checks e.User against the grants. presented is what identify
// reported: on a bearer host every request but the token one needs
// credentials, an anonymous token included. It reports false after
// answering 401 or 403.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, host string, p Parsed, e *api.LogEntry, presented bool) bool {
	var d rules.Decision
	s.store.View(func(st *state.State) {
		bearer := rules.Scheme(st, host) == api.SchemeBearer
		switch {
		case p.Kind == api.KindToken:
		case bearer && !presented:
			d = rules.Challenge
		case p.Kind == api.KindPing || p.Kind == api.KindCatalog:
			if !bearer && e.User == "" && rules.Challenges(st, host) {
				d = rules.Challenge
			}
		default:
			d = rules.Authorize(st, host, p.Repo, rules.ActionFor(p.Kind, r.Method), e.User)
		}
	})
	switch d {
	case rules.Allow:
	case rules.Challenge:
		s.unauthorized(w, r, host, p, "authentication required")
	case rules.Deny:
		writeError(w, r, http.StatusForbidden, codeDenied, "access denied")
	}
	return d == rules.Allow
}

// checkPassword reports whether name and pw are valid credentials.
func (s *Server) checkPassword(name, pw string) (valid bool) {
	s.store.View(func(st *state.State) { valid = rules.CheckPassword(st, name, pw) })
	return valid
}

// unauthorized answers 401 with the challenge for host.
func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request, host string, p Parsed, msg string) {
	w.Header().Set("WWW-Authenticate", s.challenge(r, host, p))
	writeError(w, r, http.StatusUnauthorized, codeUnauthorized, msg)
}

// bearer reports whether host uses the bearer scheme.
func (s *Server) bearer(host string) (b bool) {
	s.store.View(func(st *state.State) { b = rules.Scheme(st, host) == api.SchemeBearer })
	return b
}

// challenge returns the WWW-Authenticate value for host.
func (s *Server) challenge(r *http.Request, host string, p Parsed) string {
	if !s.bearer(host) {
		return `Basic realm="kuik-tester"`
	}
	c := fmt.Sprintf(`Bearer realm="%s://%s/token",service="%s"`, s.scheme(r), r.Host, r.Host)
	if p.Repo != "" {
		c += fmt.Sprintf(`,scope="repository:%s:%s"`, p.Repo, rules.ActionFor(p.Kind, r.Method))
	}
	return c
}
