package rules

import (
	"maps"
	"slices"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Request is what rules match on.
type Request struct {
	Host, Repo, Tag string
	Kind            api.Kind
	Method          string
	// User is "" for an anonymous request.
	User string
}

// Evaluate returns a copy of the first enabled rule matching r, or nil.
func Evaluate(st *state.State, r Request) *api.Rule {
	for i := range st.Rules {
		rule := st.Rules[i]
		if !rule.Enabled || !Matches(rule.Target, r) {
			continue
		}
		rule.Target.Kinds = slices.Clone(rule.Target.Kinds)
		rule.Target.Methods = slices.Clone(rule.Target.Methods)
		rule.Effects.Headers = maps.Clone(rule.Effects.Headers)
		return &rule
	}
	return nil
}

// Matches reports whether target t selects request r.
func Matches(t api.Target, r Request) bool {
	if !match.Label(t.Host, r.Host) || !match.Repo(t.Repo, r.Repo) {
		return false
	}
	if len(t.Kinds) > 0 && !slices.Contains(t.Kinds, r.Kind) {
		return false
	}
	if len(t.Methods) > 0 && !slices.ContainsFunc(t.Methods, func(m string) bool { return strings.EqualFold(m, r.Method) }) {
		return false
	}
	switch t.Caller {
	case "":
	case api.Anonymous:
		if r.User != "" {
			return false
		}
	default:
		if t.Caller != r.User {
			return false
		}
	}
	if r.Tag == "" && t.Tag != "" && t.Tag != "*" {
		// A request by digest matches a tag pattern only when the rule
		// explicitly targets manifest-by-digest.
		return slices.Contains(t.Kinds, api.KindManifestByDigest)
	}
	return match.Label(t.Tag, r.Tag)
}
