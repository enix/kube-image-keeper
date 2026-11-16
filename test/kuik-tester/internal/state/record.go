package state

import (
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
)

// Kinds of the digest index entries.
const (
	DigestIndex    = "index"
	DigestManifest = "manifest"
	DigestConfig   = "config"
)

// Bounds of the state RecordImage grows from request content.
const (
	maxSeen = 10_000
	// An image records at most 5 digests: its index, 2 platform manifests
	// and their 2 configs.
	maxDigests = 5 * maxSeen
)

// RecordImage adds the digests of idx to the digest index and host/repo:tag
// to the seen images (tag may be empty), and marks them seen now. It writes
// the state only when something is new or a last-seen time is older than the
// store's refresh interval: at most once per image per interval.
//
// Both sets are bounded: at maxSeen seen images, a new one replaces the
// oldest; at maxDigests digests, arbitrary entries other than those of idx
// make room for the new ones. An evicted digest of a tag still in use is
// recorded again on that tag's next pull.
//
// When the state file cannot be written, the image is still published and
// logged; the next successful write persists it.
func (st *Store) RecordImage(host, repo, tag, contentKey string, gen int, idx v1.ImageIndex) error {
	entries := map[string]DigestEntry{}
	d, err := idx.Digest()
	if err != nil {
		return err
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return err
	}
	// The platform list travels with every digest of the index, so a digest
	// still resolves after a platform rule changes what the tag serves.
	var plats []string
	if len(im.Manifests) != len(synth.Platforms) {
		for _, m := range im.Manifests {
			plats = append(plats, synth.PlatformName(*m.Platform))
		}
	}
	entries[d.String()] = DigestEntry{ContentKey: contentKey, Generation: gen, Kind: DigestIndex, Platforms: plats}
	for _, m := range im.Manifests {
		plat := synth.PlatformName(*m.Platform)
		entries[m.Digest.String()] = DigestEntry{ContentKey: contentKey, Generation: gen, Kind: DigestManifest, Platform: plat, Platforms: plats}
		img, err := idx.Image(m.Digest)
		if err != nil {
			return err
		}
		cn, err := img.ConfigName()
		if err != nil {
			return err
		}
		entries[cn.String()] = DigestEntry{ContentKey: contentKey, Generation: gen, Kind: DigestConfig, Platform: plat, Platforms: plats}
	}
	seenKey := ""
	if tag != "" {
		seenKey = RefKey(host, repo, tag)
	}
	staleBefore, refreshing := st.staleBefore()
	missing := func(s *State) bool {
		if seenKey != "" {
			t, ok := s.Seen[seenKey]
			if !ok || (refreshing && t.Before(staleBefore)) {
				return true
			}
		}
		for k := range entries {
			e, ok := s.Digests[k]
			if !ok || (refreshing && e.LastSeen.Before(staleBefore)) {
				return true
			}
		}
		return false
	}
	now := time.Now().UTC()
	var need bool
	st.View(func(s *State) { need = missing(s) })
	if !need {
		return nil
	}
	return st.update(func(s *State) error {
		added := 0
		for k := range entries {
			if _, ok := s.Digests[k]; !ok {
				added++
			}
		}
		if over := len(s.Digests) + added - maxDigests; over > 0 {
			evictDigests(s, entries, over)
		}
		for k, e := range entries {
			if old, ok := s.Digests[k]; ok {
				e = old
			}
			e.LastSeen = now
			s.Digests[k] = e
		}
		if seenKey != "" {
			if _, ok := s.Seen[seenKey]; !ok && len(s.Seen) >= maxSeen {
				evictOldestSeen(s)
			}
			s.Seen[seenKey] = now
		}
		return nil
	}, true)
}

// evictOldestSeen removes the seen image with the earliest time, the
// smallest key among equal times.
func evictOldestSeen(s *State) {
	var oldest string
	var at time.Time
	for k, t := range s.Seen {
		if oldest == "" || t.Before(at) || (t.Equal(at) && k < oldest) {
			oldest, at = k, t
		}
	}
	delete(s.Seen, oldest)
}

// evictDigests removes n entries of the digest index whose key is not in
// keep, or every such entry when there are fewer.
func evictDigests(s *State, keep map[string]DigestEntry, n int) {
	for k := range s.Digests {
		if n <= 0 {
			return
		}
		if _, ok := keep[k]; !ok {
			delete(s.Digests, k)
			n--
		}
	}
}

// TouchDigest marks a served digest seen now when its last-seen time is older
// than the store's refresh interval, so a digest still pulled by digest does
// not expire. It does nothing for an unknown digest. Like RecordImage, it
// keeps the new state in memory when the file cannot be written.
func (st *Store) TouchDigest(digest string) {
	staleBefore, refreshing := st.staleBefore()
	if !refreshing {
		return
	}
	var need bool
	st.View(func(s *State) {
		e, ok := s.Digests[digest]
		need = ok && e.LastSeen.Before(staleBefore)
	})
	if !need {
		return
	}
	now := time.Now().UTC()
	_ = st.update(func(s *State) error {
		if e, ok := s.Digests[digest]; ok {
			e.LastSeen = now
			s.Digests[digest] = e
		}
		return nil
	}, true)
}
