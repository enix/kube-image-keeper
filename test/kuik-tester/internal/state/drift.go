package state

import (
	"slices"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Generation is the current generation of host/repo:tag (default 1).
func (st *State) Generation(host, repo, tag string) int {
	if g, ok := st.Generations[RefKey(host, repo, tag)]; ok && g > 0 {
		return g
	}
	return 1
}

// Drift bumps the generation of every known host/repo:tag matched by t, so
// the tag resolves to new digests. It mutates st; callers run it inside
// Store.Update, whose maps are never nil. If nothing matched and t is a
// fully literal reference, that reference is set to generation 2. It
// returns the changed keys, sorted. Every changed key is marked seen now, so
// its generation has a clock for expiry.
func (st *State) Drift(t api.Target) []string {
	keys := map[string]struct{}{}
	for k := range st.Seen {
		keys[k] = struct{}{}
	}
	for k := range st.Generations {
		keys[k] = struct{}{}
	}
	var changed []string
	for k := range keys {
		host, repo, tag := SplitRefKey(k)
		if match.Label(t.Host, host) && match.Repo(t.Repo, repo) && match.Label(t.Tag, tag) {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 {
		if !match.Literal(t.Host) || !match.Literal(t.Repo) || !match.Literal(t.Tag) {
			return nil
		}
		k := RefKey(t.Host, t.Repo, t.Tag)
		st.Generations[k] = 2
		st.Seen[k] = time.Now().UTC()
		return []string{k}
	}
	slices.Sort(changed)
	now := time.Now().UTC()
	for _, k := range changed {
		host, repo, tag := SplitRefKey(k)
		st.Generations[k] = st.Generation(host, repo, tag) + 1
		st.Seen[k] = now
	}
	return changed
}
