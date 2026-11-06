package fakeregistry

import (
	"io"
	"maps"
	"net/http"
	"slices"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/rules"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func (s *Server) serveSynthetic(w http.ResponseWriter, r *http.Request, host string, p Parsed) {
	switch {
	case p.Kind == api.KindUpload || p.Kind == api.KindManifestPut || p.Kind == api.KindDelete:
		writeError(w, r, http.StatusMethodNotAllowed, codeUnsupported, "synthetic registries are read-only")
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		writeError(w, r, http.StatusMethodNotAllowed, codeUnsupported, "method not allowed")
	case p.Kind == api.KindPing:
		writeBody(w, r, "application/json", "", []byte("{}"))
	case p.Kind == api.KindManifestByTag:
		s.manifestByTag(w, r, host, p)
	case p.Kind == api.KindManifestByDigest:
		s.manifestByDigest(w, r, host, p)
	case p.Kind == api.KindBlob:
		s.blob(w, r, p)
	case p.Kind == api.KindTagsList:
		s.tagsList(w, r, host, p)
	case p.Kind == api.KindCatalog:
		s.catalog(w, r, host)
	default:
		// A token request is never routed here.
		writeError(w, r, http.StatusNotFound, codeNameUnknown, "unknown endpoint")
	}
}

func (s *Server) manifestByTag(w http.ResponseWriter, r *http.Request, host string, p Parsed) {
	var key string
	var gen int
	var plats []v1.Platform
	s.store.View(func(st *state.State) {
		key = rules.ContentKey(st, host, p.Repo)
		gen = st.Generation(host, p.Repo, p.Tag)
		plats = rules.Platforms(st, host, p.Repo)
	})
	idx, err := synth.Build(key, gen, plats)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeUnknown, err.Error())
		return
	}
	if err := s.store.RecordImage(host, p.Repo, p.Tag, key, gen, idx); err != nil {
		writeError(w, r, http.StatusInternalServerError, codeUnknown, err.Error())
		return
	}
	writeIndex(w, r, idx)
}

func writeIndex(w http.ResponseWriter, r *http.Request, idx v1.ImageIndex) {
	b, _ := idx.RawManifest()
	d, _ := idx.Digest()
	writeBody(w, r, string(types.OCIImageIndex), d.String(), b)
}

func (s *Server) manifestByDigest(w http.ResponseWriter, r *http.Request, host string, p Parsed) {
	var e state.DigestEntry
	var ok bool
	s.store.View(func(st *state.State) {
		e, ok = st.Digests[p.Digest]
		ok = ok && (e.Kind == state.DigestIndex || e.Kind == state.DigestManifest) && e.ContentKey == rules.ContentKey(st, host, p.Repo)
	})
	if !ok {
		writeError(w, r, http.StatusNotFound, codeManifestUnknown, "manifest unknown")
		return
	}
	s.store.TouchDigest(p.Digest)
	idx, err := synth.Build(e.ContentKey, e.Generation, synth.Select(e.Platforms))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeUnknown, err.Error())
		return
	}
	if e.Kind == state.DigestIndex {
		// A rebuild that gives another digest no longer is this index.
		if d, _ := idx.Digest(); d.String() != p.Digest {
			writeError(w, r, http.StatusNotFound, codeManifestUnknown, "manifest unknown")
			return
		}
		writeIndex(w, r, idx)
		return
	}
	h, _ := v1.NewHash(p.Digest)
	img, err := idx.Image(h)
	if err != nil {
		writeError(w, r, http.StatusNotFound, codeManifestUnknown, "manifest unknown")
		return
	}
	b, _ := img.RawManifest()
	writeBody(w, r, string(types.OCIManifestSchema1), p.Digest, b)
}

func (s *Server) blob(w http.ResponseWriter, r *http.Request, p Parsed) {
	for _, pl := range synth.Platforms {
		l, err := synth.Layer(pl.Architecture)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, codeUnknown, err.Error())
			return
		}
		if d, _ := l.Digest(); d.String() == p.Digest {
			rc, err := l.Compressed()
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, codeUnknown, err.Error())
				return
			}
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			writeBody(w, r, "application/octet-stream", p.Digest, b)
			return
		}
	}
	var e state.DigestEntry
	var ok bool
	s.store.View(func(st *state.State) { e, ok = st.Digests[p.Digest] })
	if ok && e.Kind == state.DigestConfig {
		if b, found := configBlob(e, p.Digest); found {
			s.store.TouchDigest(p.Digest)
			writeBody(w, r, "application/octet-stream", p.Digest, b)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, codeBlobUnknown, "blob unknown")
}

func configBlob(e state.DigestEntry, digest string) ([]byte, bool) {
	idx, err := synth.Build(e.ContentKey, e.Generation, synth.Select(e.Platforms))
	if err != nil {
		return nil, false
	}
	im, _ := idx.IndexManifest()
	for _, m := range im.Manifests {
		img, err := idx.Image(m.Digest)
		if err != nil {
			continue
		}
		if cn, _ := img.ConfigName(); cn.String() == digest {
			b, _ := img.RawConfigFile()
			return b, true
		}
	}
	return nil, false
}

func (s *Server) tagsList(w http.ResponseWriter, r *http.Request, host string, p Parsed) {
	tags := map[string]bool{}
	s.store.View(func(st *state.State) {
		for k := range st.Seen {
			if h, repo, tag := state.SplitRefKey(k); h == host && repo == p.Repo {
				tags[tag] = true
			}
		}
		for _, rule := range st.Rules {
			t := rule.Target
			if t.Host == host && t.Repo == p.Repo && match.Literal(t.Tag) {
				tags[t.Tag] = true
			}
		}
	})
	writeJSON(w, r, map[string]any{"name": p.Repo, "tags": sortedKeys(tags)})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, host string) {
	repos := map[string]bool{}
	s.store.View(func(st *state.State) {
		for k := range st.Seen {
			if h, repo, _ := state.SplitRefKey(k); h == host {
				repos[repo] = true
			}
		}
	})
	writeJSON(w, r, map[string]any{"repositories": sortedKeys(repos)})
}

// sortedKeys returns the keys of set in order, never nil so that JSON
// encodes an empty set as [].
func sortedKeys(set map[string]bool) []string {
	out := slices.AppendSeq(make([]string, 0, len(set)), maps.Keys(set))
	slices.Sort(out)
	return out
}
