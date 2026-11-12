package fakeregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// drift may run outside the spec goroutine: such a goroutine must defer GinkgoRecover.
func drift(store *state.Store, tg api.Target) {
	GinkgoHelper()
	Expect(store.Update(func(s *state.State) error { s.Drift(tg); return nil })).To(Succeed())
}

var _ = Describe("Drift, aliases and divergent hosts", func() {
	It("keeps the old digest pullable after a drift", func() {
		srv, store, _ := newServer()
		d1 := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		drift(store, api.Target{Host: upstream, Repo: repo, Tag: "t1"})
		d2 := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		Expect(d2).NotTo(Equal(d1), "digest did not change after drift")
		Expect(indexDigest(srv, "upstream.kt.test/acme/foo@"+d1.String())).To(Equal(d1), "old digest served")
	})

	It("answers 404 MANIFEST_UNKNOWN for a pinned digest on a divergent host", func() {
		srv, store, _ := newServer()
		d1 := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		setState(store, func(s *state.State) {
			s.Divergents = append(s.Divergents, api.Divergent{ID: "d", Host: "alt1", Repo: "acme/*", Salt: "x"})
		})
		_, err := remote.Index(testutil.Ref("alt1.kt.test/acme/foo@"+d1.String()), testutil.Opts(srv)...)
		te, ok := errors.AsType[*transport.Error](err)
		Expect(ok).To(BeTrue(), "err = %v", err)
		Expect(te.StatusCode).To(Equal(404))
		Expect(te.Errors).NotTo(BeEmpty())
		Expect(string(te.Errors[0].Code)).To(Equal(codeManifestUnknown))
	})

	It("serves the same digest through an alias as at its target", func() {
		srv, store, _ := newServer()
		setState(store, func(s *state.State) {
			s.Aliases = append(s.Aliases, api.Alias{ID: "a", Host: "alt1", From: "mirror/acme", To: "acme"})
		})
		a := indexDigest(srv, "alt1.kt.test/mirror/acme/foo:t1")
		b := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		Expect(a).To(Equal(b), "alias != upstream")
	})

	It("keeps concurrent pulls consistent while the tag drifts", func() {
		srv, store, _ := newServer()
		var wg sync.WaitGroup
		start := make(chan struct{})
		digests := make(chan string, 10)
		ref := testutil.Ref("upstream.kt.test/acme/foo:t1")
		for range 10 {
			wg.Go(func() {
				defer GinkgoRecover()
				<-start
				idx := pullIndex(srv, ref)
				d, err := idx.Digest()
				Expect(err).NotTo(HaveOccurred())
				digests <- d.String()
				checkManifestMatchesDigest(srv)
				checkFirstChildServed(srv, idx)
			})
		}
		wg.Go(func() {
			defer GinkgoRecover()
			<-start
			for range 10 {
				drift(store, api.Target{Host: upstream, Repo: repo, Tag: "t1"})
			}
		})
		close(start)
		wg.Wait()
		close(digests)
		store.View(func(st *state.State) {
			for d := range digests {
				e, ok := st.Digests[d]
				Expect(ok).To(BeTrue(), "digest %s", d)
				Expect(e.Kind).To(Equal(state.DigestIndex), "digest %s", d)
			}
			Expect(st.Generations["upstream/acme/foo:t1"]).To(Equal(11), "generation")
		})
	})
})

// pullIndex pulls the index of ref from srv.
func pullIndex(srv *httptest.Server, ref name.Reference) v1.ImageIndex {
	GinkgoHelper()
	idx, err := remote.Index(ref, testutil.Opts(srv)...)
	Expect(err).NotTo(HaveOccurred())
	return idx
}

// checkManifestMatchesDigest fetches the raw manifest of the tag and checks
// that its Docker-Content-Digest header is the hash of the body.
func checkManifestMatchesDigest(srv *httptest.Server) {
	GinkgoHelper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+manifest1, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Host = upstreamFQDN
	resp, err := srv.Client().Do(req)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	Expect(err).NotTo(HaveOccurred())
	sum := sha256.Sum256(body)
	Expect(resp.Header.Get("Docker-Content-Digest")).To(Equal("sha256:"+hex.EncodeToString(sum[:])), "Docker-Content-Digest vs body hash")
}

// checkFirstChildServed checks that the first child of idx, fetched after a
// possible drift, is still served.
func checkFirstChildServed(srv *httptest.Server, idx v1.ImageIndex) {
	GinkgoHelper()
	im, err := idx.IndexManifest()
	Expect(err).NotTo(HaveOccurred(), "index manifest")
	Expect(im.Manifests).NotTo(BeEmpty(), "index manifest has no children")
	child := "upstream.kt.test/acme/foo@" + im.Manifests[0].Digest.String()
	cref, err := name.ParseReference(child, name.Insecure)
	Expect(err).NotTo(HaveOccurred())
	_, err = remote.Image(cref, testutil.Opts(srv)...)
	Expect(err).NotTo(HaveOccurred(), "child %s", child)
}
