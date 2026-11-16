package rules

import (
	"crypto/subtle"
	"net/http"
	"slices"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Decision is the outcome of an authorization check.
type Decision int

const (
	Allow     Decision = iota
	Challenge          // 401: credentials are required
	Deny               // 403: the user is not allowed
)

// ActionFor maps a request to the grant action it needs.
func ActionFor(kind api.Kind, method string) api.Action {
	switch {
	case method == http.MethodDelete || kind == api.KindDelete:
		return api.ActionDelete
	case method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut:
		return api.ActionPush
	}
	return api.ActionPull
}

// Authorize checks user ("" for anonymous) against every grant covering
// host, repo and action a. Matching grants unite their users. Without a
// matching grant the request is allowed.
func Authorize(st *state.State, host, repo string, a api.Action, user string) Decision {
	restricted := false
	for _, g := range st.Grants {
		if !match.Label(g.Host, host) || !match.Repo(g.Repo, repo) || !slices.Contains(g.Actions, a) {
			continue
		}
		restricted = true
		if slices.Contains(g.Users, user) && user != "" {
			return Allow
		}
	}
	switch {
	case !restricted:
		return Allow
	case user == "":
		return Challenge
	}
	return Deny
}

// Challenges reports whether anonymous requests to host's ping and
// catalog are answered 401: a grant covers the host or it uses bearer.
func Challenges(st *state.State, host string) bool {
	for _, g := range st.Grants {
		if match.Label(g.Host, host) {
			return true
		}
	}
	return Scheme(st, host) == api.SchemeBearer
}

// CheckPassword reports whether password is user's password.
func CheckPassword(st *state.State, user, password string) bool {
	for _, u := range st.Users {
		if u.Name == user {
			return subtle.ConstantTimeCompare([]byte(u.Password), []byte(password)) == 1
		}
	}
	return false
}

// Scheme returns the auth scheme of host, api.SchemeBasic unless a HostAuth says
// otherwise.
func Scheme(st *state.State, host string) api.Scheme {
	for _, h := range st.HostAuth {
		if match.Label(h.Host, host) {
			return h.Scheme
		}
	}
	return api.SchemeBasic
}
