package admin

import (
	"slices"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var (
	httpMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	grantActs   = []api.Action{api.ActionPull, api.ActionPush, api.ActionDelete}
)

// validateState checks the whole candidate state, which must already be
// normalised by normalizeState (it does not normalise it itself). It never
// writes the state. The first problem found is returned, naming the item.
func validateState(st *state.State) error {
	known := refs{users: userColl.keys(st), quotas: quotaColl.keys(st)}
	for _, c := range collections {
		if err := c.validate(st, known); err != nil {
			return err
		}
	}
	for k, g := range st.Generations {
		h, r, t := state.SplitRefKey(k)
		if g < 1 || h == "" || r == "" || t == "" {
			return invalid("generation %q: want host/repo:tag and a value of at least 1", k)
		}
	}
	return nil
}

func checkUser(u api.User, _ refs) error {
	switch {
	case u.Name == "":
		return invalid("user: empty name")
	case u.Name == api.Anonymous || strings.Contains(u.Name, "(invalid)") || strings.Contains(u.Name, ":"):
		return invalid("user %q: reserved or forbidden name", u.Name)
	case u.Password == "":
		return invalid("user %q: empty password", u.Name)
	}
	return nil
}

func checkQuota(q api.QuotaDef, _ refs) error {
	switch {
	case q.Name == "" || strings.Contains(q.Name, "/"):
		return invalid("quota %q: name must be non-empty and without '/'", q.Name)
	case q.Limit < 1:
		return invalid("quota %q: limit must be at least 1", q.Name)
	case q.WindowSec < 1:
		return invalid("quota %q: windowSec must be at least 1", q.Name)
	case !q.Key.Valid():
		return invalid("quota %q: key must be global, ip or user", q.Name)
	}
	return checkKindsMethods("quota "+q.Name, q.Kinds, q.Methods)
}

func checkRule(r api.Rule, known refs) error {
	who := "rule " + r.ID
	t := r.Target
	if err := validGlobs(who, t); err != nil {
		return err
	}
	if err := checkKindsMethods(who, t.Kinds, t.Methods); err != nil {
		return err
	}
	if t.Caller != "" && t.Caller != api.Anonymous && !known.users[t.Caller] {
		return invalid("%s: caller %q is not a user", who, t.Caller)
	}
	e := r.Effects
	switch {
	case e.Status != 0 && (e.Status < 400 || e.Status > 599):
		return invalid("%s: status %d out of 400-599", who, e.Status)
	case e.LatencyMS < 0 || e.LatencyMS > 600000:
		return invalid("%s: latencyMs %d out of 0-600000", who, e.LatencyMS)
	case e.Quota != "" && !known.quotas[e.Quota]:
		return invalid("%s: unknown quota %q", who, e.Quota)
	case e.Pass && (e.Status != 0 || e.Reset):
		return invalid("%s: pass cannot be combined with status or reset", who)
	}
	for name, v := range e.Headers {
		if !isToken(name) {
			return invalid("%s: invalid header name %q", who, name)
		}
		if slices.ContainsFunc(framingHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			return invalid("%s: header %s cannot be set by a rule", who, name)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return invalid("%s: invalid value for header %s", who, name)
		}
	}
	return nil
}

// framingHeaders are the headers the HTTP layer owns: a rule setting one
// would corrupt the framing of the response.
var framingHeaders = []string{"Content-Length", "Transfer-Encoding", "Connection", "Host", "Upgrade", "TE"}

func checkAlias(a api.Alias, _ refs) error {
	if err := match.Valid(a.Host); err != nil {
		return invalid("alias %s: invalid host glob %q", a.ID, a.Host)
	}
	if !repoPath(a.From) || !repoPath(a.To) {
		return invalid("alias %s: from and to must be literal repository paths", a.ID)
	}
	return nil
}

func checkDivergent(d api.Divergent, _ refs) error {
	if err := checkScoped("divergent", d.ID, d.Host, d.Repo); err != nil {
		return err
	}
	if d.Salt == "" {
		return invalid("divergent %s: empty salt", d.ID)
	}
	return nil
}

func checkGrant(g api.Grant, known refs) error {
	if err := checkScoped("grant", g.ID, g.Host, g.Repo); err != nil {
		return err
	}
	if len(g.Actions) == 0 || len(g.Users) == 0 {
		return invalid("grant %s: actions and users must not be empty", g.ID)
	}
	for _, a := range g.Actions {
		if !slices.Contains(grantActs, a) {
			return invalid("grant %s: unknown action %q", g.ID, a)
		}
	}
	for _, u := range g.Users {
		if !known.users[u] {
			return invalid("grant %s: unknown user %q", g.ID, u)
		}
	}
	return nil
}

func checkHostAuth(h api.HostAuth, _ refs) error {
	switch {
	case h.Host == "" || match.Valid(h.Host) != nil:
		return invalid("hostAuth %q: invalid host glob", h.Host)
	case !h.Scheme.Valid():
		return invalid("hostAuth %q: scheme must be basic or bearer", h.Host)
	}
	return nil
}

// checkPlatform checks a platform rule: valid host and repo globs, a
// non-empty list of known platform names without duplicates.
func checkPlatform(p api.PlatformRule, _ refs) error {
	if err := checkScoped("platform rule", p.ID, p.Host, p.Repo); err != nil {
		return err
	}
	if len(p.Platforms) == 0 {
		return invalid("platform rule %s: at least one platform is required", p.ID)
	}
	seen := map[string]bool{}
	for _, n := range p.Platforms {
		if len(synth.Select([]string{n})) == 0 {
			return invalid("platform rule %s: unknown platform %q", p.ID, n)
		}
		if seen[n] {
			return invalid("platform rule %s: duplicate platform %q", p.ID, n)
		}
		seen[n] = true
	}
	return nil
}

// checkScoped checks the host and repo globs of an ID-keyed object.
func checkScoped(what, id, host, repo string) error {
	if match.Valid(host) != nil || match.Valid(repo) != nil {
		return invalid("%s %s: invalid host or repo glob", what, id)
	}
	return nil
}

// validGlobs checks the host, repo and tag patterns of a target.
func validGlobs(who string, t api.Target) error {
	if match.Valid(t.Host) != nil || strings.Contains(t.Host, "/") {
		return invalid("%s: invalid host glob %q", who, t.Host)
	}
	if match.Valid(t.Repo) != nil {
		return invalid("%s: invalid repo glob %q", who, t.Repo)
	}
	if match.Valid(t.Tag) != nil || strings.Contains(t.Tag, "/") {
		return invalid("%s: invalid tag glob %q", who, t.Tag)
	}
	return nil
}

// checkKindsMethods checks the kinds and methods of a target. Methods
// compare exactly: normalisation upper-cases them beforehand.
func checkKindsMethods(who string, kinds []api.Kind, methods []string) error {
	for _, k := range kinds {
		if !k.Valid() {
			return invalid("%s: unknown kind %q", who, k)
		}
	}
	for _, m := range methods {
		if !slices.Contains(httpMethods, m) {
			return invalid("%s: unknown method %q", who, m)
		}
	}
	return nil
}

// repoPath reports a non-empty repository path with no glob character and
// no empty, "." or ".." segment.
func repoPath(s string) bool {
	if !match.Literal(s) {
		return false
	}
	for seg := range strings.SplitSeq(s, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// isToken reports an RFC 9110 token, the grammar of header names.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// RuleWarnings lists the non-blocking problems of a rule; never nil.
func RuleWarnings(r api.Rule) []string {
	out := []string{}
	if r.Target.Caller == api.Anonymous && (r.Effects.Status != 0 || r.Effects.Reset) {
		out = append(out, "caller anonymous also matches the first, unauthenticated request (ping, /token) of clients that authenticate")
	}
	return out
}
