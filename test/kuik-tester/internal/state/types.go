// Package state holds the dynamic state edited from the admin UI and API.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// DigestEntry records what a served digest was built from.
type DigestEntry struct {
	ContentKey string `json:"contentKey"`
	Generation int    `json:"generation"`
	// Kind is DigestIndex, DigestManifest or DigestConfig.
	Kind     string `json:"kind"`
	Platform string `json:"platform,omitempty"`
	// Platforms is the platform list of the index the digest belongs to,
	// empty when it is the full list.
	Platforms []string `json:"platforms,omitempty"`
	// LastSeen is when the digest was recorded or last served, to within
	// the store's refresh interval. Zero in a state file written before
	// expiry existed.
	LastSeen time.Time `json:"lastSeen,omitempty"`
}

// State is the whole dynamic state.
type State struct {
	Rules       []api.Rule             `json:"rules"`
	Aliases     []api.Alias            `json:"aliases"`
	Divergents  []api.Divergent        `json:"divergents"`
	Users       []api.User             `json:"users"`
	Grants      []api.Grant            `json:"grants"`
	HostAuth    []api.HostAuth         `json:"hostAuth"`
	Quotas      []api.QuotaDef         `json:"quotas"`
	Platforms   []api.PlatformRule     `json:"platforms"`
	Generations map[string]int         `json:"generations"`
	Digests     map[string]DigestEntry `json:"digests"`
	// Seen maps a host/repo:tag to when it was last seen, to within the
	// store's refresh interval.
	Seen map[string]time.Time `json:"seen"`
}

func (s *State) init() {
	if s.Generations == nil {
		s.Generations = map[string]int{}
	}
	if s.Digests == nil {
		s.Digests = map[string]DigestEntry{}
	}
	if s.Seen == nil {
		s.Seen = map[string]time.Time{}
	}
}

// RefKey identifies a host/repo:tag in Generations and Seen.
func RefKey(host, repo, tag string) string {
	return host + "/" + repo + ":" + tag
}

// SplitRefKey reverses RefKey.
func SplitRefKey(k string) (host, repo, tag string) {
	host, rest, _ := strings.Cut(k, "/")
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return host, rest[:i], rest[i+1:]
	}
	return host, rest, ""
}

// NewID returns 8 random hex characters.
func NewID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
