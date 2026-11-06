// Package rules evaluates rules and settings against registry requests.
package rules

import (
	"path"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
)

// ContentKey is what a host/repo serves: two requests with the same content
// key and generation get the same digests.
//
// The first matching alias rewrites the path prefix; the first matching
// divergent rule (matched against the request repo, not the aliased key)
// then appends its salt.
func ContentKey(st *state.State, host, repo string) string {
	key := repo
	for _, a := range st.Aliases {
		if !match.Label(a.Host, host) {
			continue
		}
		if rest, ok := match.TrimPrefix(repo, a.From); ok {
			key = path.Join(a.To, rest)
			break
		}
	}
	for _, d := range st.Divergents {
		if match.Label(d.Host, host) && match.Repo(d.Repo, repo) {
			key += "#" + d.Salt
			break
		}
	}
	return key
}

// Platforms is the platform list of host/repo: that of the first matching
// platform rule, or nil (every platform) when none matches.
func Platforms(st *state.State, host, repo string) []v1.Platform {
	for _, p := range st.Platforms {
		if match.Label(p.Host, host) && match.Repo(p.Repo, repo) {
			return synth.Select(p.Platforms)
		}
	}
	return nil
}
