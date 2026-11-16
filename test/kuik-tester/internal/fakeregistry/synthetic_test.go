package fakeregistry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/quota"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Synthetic registry", func() {
	It("serves a new image whole when the state file cannot be written", func() {
		path := filepath.Join(GinkgoT().TempDir(), "state.json")
		srv, _, _ := newServer(withStateFile(path))
		Expect(os.Mkdir(path+".tmp", 0o700)).To(Succeed())
		idx, err := remote.Index(testutil.Ref(upstreamFQDN+"/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		im, err := idx.IndexManifest()
		Expect(err).NotTo(HaveOccurred())
		Expect(im.Manifests).NotTo(BeEmpty())
		img, err := remote.Image(testutil.Ref(upstreamFQDN+"/acme/foo@"+im.Manifests[0].Digest.String()), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		_, err = img.ConfigFile()
		Expect(err).NotTo(HaveOccurred())
		Expect(path).NotTo(BeAnExistingFile(), "the write must have failed")
	})

	It("serves a first platform manifest by digest when the digest index is full", func() {
		srv, st, _ := newServer()
		// state's maxDigests (5 * maxSeen) is unexported: fill to its value.
		Expect(st.Update(func(s *state.State) error {
			for i := range 50_000 {
				s.Digests[fmt.Sprintf("sha256:%064x", i)] = state.DigestEntry{Kind: state.DigestConfig}
			}
			return nil
		})).To(Succeed())
		idx, err := remote.Index(testutil.Ref(upstreamFQDN+"/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		im, err := idx.IndexManifest()
		Expect(err).NotTo(HaveOccurred())
		Expect(im.Manifests).NotTo(BeEmpty())
		img, err := remote.Image(testutil.Ref(upstreamFQDN+"/acme/foo@"+im.Manifests[0].Digest.String()), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		_, err = img.ConfigFile()
		Expect(err).NotTo(HaveOccurred())
	})

	It("serves a two-platform index whose layers are the synth layers", func() {
		srv, _, _ := newServer()
		ref := testutil.Ref("upstream.kt.test/acme/foo:t1")
		idx, err := remote.Index(ref, testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		im, err := idx.IndexManifest()
		Expect(err).NotTo(HaveOccurred())
		Expect(im.Manifests).To(HaveLen(2))
		img, err := remote.Image(ref, append(testutil.Opts(srv), remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "arm64"}))...)
		Expect(err).NotTo(HaveOccurred())
		layers, err := img.Layers()
		Expect(err).NotTo(HaveOccurred())
		rc, err := layers[0].Compressed()
		Expect(err).NotTo(HaveOccurred())
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		layer, err := synth.Layer("arm64")
		Expect(err).NotTo(HaveOccurred())
		wrc, _ := layer.Compressed()
		want, _ := io.ReadAll(wrc)
		Expect(bytes.Equal(got, want)).To(BeTrue(), "arm64 layer bytes differ from synth.Layer(arm64)")
		_, err = img.ConfigFile()
		Expect(err).NotTo(HaveOccurred(), "config")
	})

	It("serves the same content on every host", func() {
		srv, _, _ := newServer()
		a, b := indexDigest(srv, "upstream.kt.test/acme/foo:t1"), indexDigest(srv, "alt1.kt.test/acme/foo:t1")
		Expect(a).To(Equal(b), "upstream != alt1")
	})

	It("answers HEAD with the headers of GET and no body", func() {
		srv, _, _ := newServer()
		head, hb := raw(srv, http.MethodHead, upstreamFQDN, manifest1, nil)
		get, gb := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		Expect(head.StatusCode).To(Equal(200), "HEAD")
		Expect(get.StatusCode).To(Equal(200), "GET")
		for _, h := range []string{"Docker-Content-Digest", headerContentType, headerContentLength} {
			Expect(head.Header.Get(h)).To(Equal(get.Header.Get(h)), h)
			Expect(get.Header.Get(h)).NotTo(BeEmpty(), h)
		}
		Expect(hb).To(BeEmpty(), "HEAD body")
		sum := sha256.Sum256(gb)
		Expect(get.Header.Get("Docker-Content-Digest")).To(Equal("sha256:" + hex.EncodeToString(sum[:])))
		Expect(get.Header.Get(headerContentType)).To(Equal("application/vnd.oci.image.index.v1+json"))
	})

	It("ignores the Accept header", func() {
		srv, _, _ := newServer()
		_, a := raw(srv, http.MethodGet, upstreamFQDN, manifest1, nil)
		_, b := raw(srv, http.MethodGet, upstreamFQDN, manifest1,
			map[string]string{"Accept": "application/vnd.docker.distribution.manifest.v2+json"})
		Expect(bytes.Equal(a, b)).To(BeTrue(), "bytes depend on Accept")
	})

	It("serves a pinned digest only on its own repo and reaches platform manifests by digest", func() {
		srv, _, _ := newServer()
		d := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		Expect(indexDigest(srv, "upstream.kt.test/acme/foo@"+d.String())).To(Equal(d), "pinned pull")
		_, err := remote.Index(testutil.Ref("upstream.kt.test/acme/bar@"+d.String()), testutil.Opts(srv)...)
		Expect(statusOf(err)).To(Equal(404), "pinned digest on another repo: %v", err)
		// A platform manifest is reachable by digest too.
		idx, _ := remote.Index(testutil.Ref("upstream.kt.test/acme/foo:t1"), testutil.Opts(srv)...)
		im, _ := idx.IndexManifest()
		resp, _ := raw(srv, http.MethodHead, upstreamFQDN, "/v2/acme/foo/manifests/"+im.Manifests[0].Digest.String(), nil)
		Expect(resp.StatusCode).To(Equal(200), "platform manifest by digest")
		Expect(resp.Header.Get(headerContentType)).To(Equal("application/vnd.oci.image.manifest.v1+json"), "platform manifest by digest")
	})

	DescribeTable("resolves the Host header",
		func(host string, want int) {
			srv, _, _ := newServer()
			resp, _ := raw(srv, http.MethodHead, host, manifest1, nil)
			Expect(resp.StatusCode).To(Equal(want), "Host %s", host)
		},
		Entry("upper-case host with a port", "UPSTREAM.kt.test:443", 200),
		Entry("host with a trailing dot", "upstream.kt.test.", 200),
		Entry("two labels under the base domain", "a.b.kt.test", 404),
		Entry("the bare base domain", baseDomain, 404),
		Entry("a foreign domain", "evil.example.com", 404),
		Entry("a label with a glob character", "we*ird.kt.test", 404),
		Entry("a 64-character label", strings.Repeat("a", 64)+".kt.test", 404),
		Entry("a 63-character label", strings.Repeat("a", 63)+".kt.test", 200),
	)

	It("records nothing for a refused host or repo", func() {
		srv, st, log := newServer()
		resp, b := raw(srv, http.MethodGet, "we*ird.kt.test", manifest1, nil)
		Expect(resp.StatusCode).To(Equal(404), "refused host")
		Expect(errorCode(b)).To(Equal("NAME_UNKNOWN"))
		resp, b = raw(srv, http.MethodGet, upstreamFQDN, "/v2/Acme/foo/manifests/t1", nil)
		Expect(resp.StatusCode).To(Equal(404), "refused repo")
		Expect(errorCode(b)).To(Equal("NAME_UNKNOWN"))
		es := waitEntries(log, 2)
		unknown := 0
		for _, e := range es {
			if e.UnknownHost {
				unknown++
			}
		}
		Expect(unknown).To(Equal(1), "entries with UnknownHost: %+v", es)
		st.View(func(s *state.State) {
			Expect(s.Seen).To(BeEmpty())
			Expect(s.Digests).To(BeEmpty())
		})
	})

	It("answers the ping", func() {
		srv, _, _ := newServer()
		resp, b := raw(srv, http.MethodGet, upstreamFQDN, "/v2/", nil)
		Expect(resp.StatusCode).To(Equal(200), "ping: %v %q", resp.Header, b)
		Expect(resp.Header.Get("Docker-Distribution-API-Version")).To(Equal("registry/2.0"), "ping: %v %q", resp.Header, b)
		Expect(strings.TrimSpace(string(b))).To(Equal("{}"), "ping: %v", resp.Header)
	})

	It("lists the tags of a repo and the catalog", func() {
		srv, st, _ := newServer()
		setState(st, func(s *state.State) {
			s.Rules = append(s.Rules, api.Rule{ID: "r", Target: api.Target{Host: upstream, Repo: repo, Tag: "t9"}})
		})
		indexDigest(srv, "upstream.kt.test/acme/foo:t2")
		indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		indexDigest(srv, "upstream.kt.test/acme/bar:t1")
		indexDigest(srv, "alt1.kt.test/acme/zzz:t1")

		_, b := raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/tags/list", nil)
		var tl struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}
		Expect(json.Unmarshal(b, &tl)).To(Succeed())
		Expect(tl.Name).To(Equal(repo), "tags/list = %+v", tl)
		Expect(strings.Join(tl.Tags, ",")).To(Equal("t1,t2,t9"), "tags/list = %+v", tl)
		_, b = raw(srv, http.MethodGet, upstreamFQDN, "/v2/_catalog", nil)
		var cat struct {
			Repositories []string `json:"repositories"`
		}
		_ = json.Unmarshal(b, &cat)
		Expect(strings.Join(cat.Repositories, ",")).To(Equal("acme/bar,acme/foo"), "catalog = %+v", cat)
	})

	DescribeTable("rejects a write",
		func(method, path string) {
			srv, _, _ := newServer()
			resp, b := raw(srv, method, upstreamFQDN, path, nil)
			Expect(resp.StatusCode).To(Equal(405), "%s %s: %s", method, path, b)
			Expect(string(b)).To(ContainSubstring("UNSUPPORTED"), "%s %s", method, path)
		},
		Entry("upload start", http.MethodPost, "/v2/acme/foo/blobs/uploads/"),
		Entry("manifest put", http.MethodPut, manifest1),
		Entry("manifest delete", http.MethodDelete, manifest1),
		Entry("unknown method on a manifest", "FOO", manifest1),
		Entry("post on a manifest", http.MethodPost, manifest1),
	)

	It("logs an unknown method under its name and counts it as OTHER", func() {
		srv, _, log := newServer()
		resp, b := raw(srv, "FOO", upstreamFQDN, manifest1, nil)
		Expect(resp.StatusCode).To(Equal(405), "body = %s", b)
		Expect(errorCode(b)).To(Equal(codeUnsupported))
		e := waitEntry(log, func(e api.LogEntry) bool { return e.Method == "FOO" })
		Expect(e.Status).To(Equal(405))
		Eventually(func() []string {
			stats := log.Stats()
			methods := make([]string, 0, len(stats))
			for _, st := range stats {
				methods = append(methods, st.Method)
			}
			return methods
		}).WithTimeout(waitTimeout).Should(ContainElement("OTHER"))
		for _, st := range log.Stats() {
			Expect(st.Method).NotTo(Equal("FOO"), "stats = %+v", log.Stats())
		}
	})

	DescribeTable("answers a persisted digest the rebuild no longer produces as unknown",
		func(kind, path, wantCode string) {
			srv, store, _ := newServer()
			stale := "sha256:" + strings.Repeat("a", 64)
			setState(store, func(s *state.State) {
				s.Digests[stale] = state.DigestEntry{ContentKey: "acme/foo", Generation: 1, Kind: kind}
			})
			resp, b := raw(srv, http.MethodGet, upstreamFQDN, fmt.Sprintf(path, stale), nil)
			Expect(resp.StatusCode).To(Equal(404), "body = %s", b)
			Expect(errorCode(b)).To(Equal(wantCode))
		},
		Entry("index", state.DigestIndex, "/v2/acme/foo/manifests/%s", codeManifestUnknown),
		Entry("manifest", state.DigestManifest, "/v2/acme/foo/manifests/%s", codeManifestUnknown),
		Entry("config", state.DigestConfig, "/v2/acme/foo/blobs/%s", codeBlobUnknown),
	)

	It("refreshes the last-seen time of a digest pulled by digest", func() {
		srv, st, _ := newServer()
		st.SetRefresh(time.Hour)
		idx, err := remote.Index(testutil.Ref(upstreamFQDN+"/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		im, err := idx.IndexManifest()
		Expect(err).NotTo(HaveOccurred())
		old := time.Now().Add(-48 * time.Hour)
		setState(st, func(s *state.State) {
			for k, e := range s.Digests {
				e.LastSeen = old
				s.Digests[k] = e
			}
		})
		manifestDigest := im.Manifests[0].Digest.String()
		resp, b := raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/manifests/"+manifestDigest, nil)
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "body = %s", b)
		img, err := idx.Image(im.Manifests[0].Digest)
		Expect(err).NotTo(HaveOccurred())
		cn, err := img.ConfigName()
		Expect(err).NotTo(HaveOccurred())
		resp, b = raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/blobs/"+cn.String(), nil)
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "body = %s", b)
		s := st.Snapshot()
		Expect(s.Digests[manifestDigest].LastSeen).To(BeTemporally("~", time.Now(), time.Minute))
		Expect(s.Digests[cn.String()].LastSeen).To(BeTemporally("~", time.Now(), time.Minute))
		Expect(s.Digests[im.Manifests[1].Digest.String()].LastSeen).To(BeTemporally("==", old))
	})

	It("answers 404 for an unknown blob and an unknown manifest", func() {
		srv, _, _ := newServer()
		dg := "sha256:" + strings.Repeat("0", 64)
		resp, b := raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/blobs/"+dg, nil)
		Expect(resp.StatusCode).To(Equal(404), "blob: %s", b)
		Expect(string(b)).To(ContainSubstring("BLOB_UNKNOWN"), "blob")
		resp, b = raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/manifests/"+dg, nil)
		Expect(resp.StatusCode).To(Equal(404), "manifest: %s", b)
		Expect(string(b)).To(ContainSubstring("MANIFEST_UNKNOWN"), "manifest")
	})

	It("still resolves an old index digest after a platform rule", func() {
		srv, st, _ := newServer()
		old := indexDigest(srv, "upstream.kt.test/acme/foo:t1")
		setState(st, func(s *state.State) {
			s.Platforms = []api.PlatformRule{{ID: "p", Host: upstream, Repo: repo, Platforms: []string{"linux/amd64"}}}
		})
		resp, body := raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/manifests/"+old.String(), nil)
		Expect(resp.StatusCode).To(Equal(200), "old index by digest: %s", body)
		var im v1.IndexManifest
		Expect(json.Unmarshal(body, &im)).To(Succeed())
		Expect(im.Manifests).To(HaveLen(2), "old index manifests")
		idx, err := remote.Index(testutil.Ref("upstream.kt.test/acme/foo:t1"), testutil.Opts(srv)...)
		Expect(err).NotTo(HaveOccurred())
		nim, _ := idx.IndexManifest()
		Expect(nim.Manifests).To(HaveLen(1), "new index manifests = %+v, want one amd64", nim.Manifests)
		Expect(nim.Manifests[0].Platform.Architecture).To(Equal("amd64"), "new index manifests = %+v", nim.Manifests)
		img, err := idx.Image(nim.Manifests[0].Digest)
		Expect(err).NotTo(HaveOccurred())
		cn, _ := img.ConfigName()
		resp, _ = raw(srv, http.MethodGet, upstreamFQDN, "/v2/acme/foo/blobs/"+cn.String(), nil)
		Expect(resp.StatusCode).To(Equal(200), "config blob of the single-platform image")
	})

	DescribeTable("maps the Host of the plain-HTTP listener to a label",
		func(host, want string) {
			st, _ := state.Open("")
			cfg := testCfg()
			Expect(cfg.Resolve()).To(Succeed())
			proxies, err := dest.NewProxies(cfg)
			Expect(err).NotTo(HaveOccurred())
			srv := httptest.NewServer(New(cfg, proxies, st, reqlog.New(cfg.LogSize), quota.New()).FixedHostHandler(plainHost))
			DeferCleanup(srv.Close)
			resp, _ := raw(srv, http.MethodHead, host, manifest1, nil)
			Expect(resp.StatusCode).To(Equal(200), host)
			st.View(func(s *state.State) {
				_, ok := s.Seen[state.RefKey(want, repo, "t1")]
				Expect(ok).To(BeTrue(), "%s: not seen as %s: %v", host, want, s.Seen)
			})
		},
		Entry("a label with the http- prefix", "http-alt15.kt.test:5000", "http-alt15"),
		Entry("an IP address", "10.0.0.5:5000", plainHost),
		Entry("a regular host", upstreamFQDN+":5000", plainHost),
		Entry("a label with the http prefix but no dash", "httpx.kt.test:5000", plainHost),
	)
})
