package fakeregistry

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// tokenTTL is the lifetime of a bearer token.
const tokenTTL = 300 * time.Second

const (
	// tokenSweepEvery is the minimum delay between two sweeps of expired tokens.
	tokenSweepEvery = 30 * time.Second
	// defaultMaxTokens bounds the live bearer tokens.
	defaultMaxTokens = 10_000
)

// tokenInfo is what a bearer token stands for. User "" is an anonymous token.
type tokenInfo struct {
	User    string
	Expires time.Time
}

// issueToken returns a new token for user. Expired tokens are swept at most
// every tokenSweepEvery; when the store is full an arbitrary token is evicted
// to make room, so issuing never fails (the evicted client re-authenticates
// on its 401).
func (s *Server) issueToken(user string, now time.Time) string {
	tok := rand.Text()
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	if !now.Before(s.nextSweep) {
		for k, v := range s.tokens {
			if !now.Before(v.Expires) {
				delete(s.tokens, k)
			}
		}
		s.nextSweep = now.Add(tokenSweepEvery)
	}
	for len(s.tokens) >= max(s.maxTokens, 1) {
		for k := range s.tokens {
			delete(s.tokens, k)
			break
		}
	}
	s.tokens[tok] = tokenInfo{User: user, Expires: now.Add(tokenTTL)}
	return tok
}

// lookupToken returns the user tok was issued to. An expired token is
// dropped and reported as unknown.
func (s *Server) lookupToken(tok string, now time.Time) (string, bool) {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	info, ok := s.tokens[tok]
	if ok && !now.Before(info.Expires) {
		delete(s.tokens, tok)
		return "", false
	}
	return info.User, ok
}

// scheme returns the scheme the client used to reach the server. From a
// trusted peer, the first X-Forwarded-Proto element wins if it is http or
// https.
func (s *Server) scheme(r *http.Request) string {
	if s.trustedPeer(r) {
		first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
		switch p := strings.ToLower(strings.TrimSpace(first)); p {
		case schemeHTTP, schemeHTTPS:
			return p
		}
	}
	if r.TLS != nil {
		return schemeHTTPS
	}
	return schemeHTTP
}

// maxTokenForm caps the body of a POST /token.
const maxTokenForm = 64 << 10

// serveToken answers /token on a bearer host. GET was already identified by
// identify (Basic, or anonymous). POST is containerd's OAuth password
// grant, which carries its credentials in the form. Scope and service are
// ignored, rights are checked per request.
func (s *Server) serveToken(w http.ResponseWriter, r *http.Request, host string, p Parsed, e *api.LogEntry) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxTokenForm)
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "password" {
			writeError(w, r, http.StatusBadRequest, codeUnsupported, "only the password grant is supported")
			return
		}
		name, pw := r.PostForm.Get("username"), r.PostForm.Get("password")
		if !s.checkPassword(name, pw) {
			e.User = strings.TrimSpace(name + " (invalid)")
			s.unauthorized(w, r, host, p, "authentication required")
			return
		}
		e.User = name
	default:
		writeError(w, r, http.StatusMethodNotAllowed, codeUnsupported, "method not allowed")
		return
	}
	now := s.now()
	tok := s.issueToken(e.User, now)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":        tok,
		"access_token": tok,
		"expires_in":   int(tokenTTL / time.Second),
		"issued_at":    now.UTC().Format(time.RFC3339),
	})
}
