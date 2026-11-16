package dest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	repo  = "acme/foo"
	tagA  = "t1_cluster-a"
	tagX  = "t1_cluster-x"
	teamA = "team/a"
)

func synthDigest(key string, gen int) string {
	GinkgoHelper()
	idx, err := synth.Build(key, gen, nil)
	Expect(err).NotTo(HaveOccurred())
	d, err := idx.Digest()
	Expect(err).NotTo(HaveOccurred())
	return d.String()
}

// backendURL parses a backend URL the way config does.
func backendURL(s string) *url.URL {
	GinkgoHelper()
	u, err := url.Parse(s)
	Expect(err).NotTo(HaveOccurred())
	return u
}

func newBackend() (*Client, *atomic.Int64) {
	GinkgoHelper()
	var hits atomic.Int64
	h := testutil.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h.ServeHTTP(w, r)
	}))
	DeferCleanup(srv.Close)
	c, err := New(backendURL(srv.URL))
	Expect(err).NotTo(HaveOccurred())
	return c, &hits
}

// newStatusBackend wraps an in-memory registry; requests for which fail is
// true get status with an empty body.
func newStatusBackend(status int, fail func(r *http.Request) bool) (*Client, *[]string) {
	GinkgoHelper()
	var mu sync.Mutex
	var seen []string
	h := testutil.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if fail(r) {
			w.WriteHeader(status)
			return
		}
		h.ServeHTTP(w, r)
	}))
	DeferCleanup(srv.Close)
	c, err := New(backendURL(srv.URL))
	Expect(err).NotTo(HaveOccurred())
	return c, &seen
}

// newFlakyBackend makes the requests for which fail is true answer 404.
func newFlakyBackend(fail func(r *http.Request) bool) (*Client, *[]string) {
	GinkgoHelper()
	return newStatusBackend(http.StatusNotFound, fail)
}

// deleteOf matches a DELETE of path.
func deleteOf(path string) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodDelete && r.URL.Path == path
	}
}

// tagsOf lists the tags of repo from the backend's inventory, nil when the
// repo is absent. A repo that is listed must carry a non-nil Tags slice,
// empty or not.
func tagsOf(ctx context.Context, c *Client, repo string) []api.TagInfo {
	GinkgoHelper()
	inv, err := c.Inventory(ctx)
	Expect(err).NotTo(HaveOccurred())
	for _, r := range inv {
		if r.Name == repo {
			Expect(r.Tags).NotTo(BeNil(), "repo %s has nil Tags", r.Name)
			return r.Tags
		}
	}
	return nil
}

// checkActions pushes, tags and deletes in repo and checks the inventory of
// that repo after each step. The in-process and the zot specs share it.
func checkActions(ctx context.Context, c *Client, repo string) {
	GinkgoHelper()
	d := synthDigest(repo, 1)
	Expect(c.PushSynthetic(ctx, repo, tagA, repo, 1)).To(Succeed())
	Expect(c.Tag(ctx, repo, tagA, tagX)).To(Succeed())
	Expect(tagsOf(ctx, c, repo)).To(Equal([]api.TagInfo{{Tag: tagA, Digest: d}, {Tag: tagX, Digest: d}}))
	Expect(c.DeleteTag(ctx, repo, tagX)).To(Succeed())
	Expect(tagsOf(ctx, c, repo)).To(Equal([]api.TagInfo{{Tag: tagA, Digest: d}}))
	Expect(c.Tag(ctx, repo, d, "t2")).To(Succeed())
	Expect(tagsOf(ctx, c, repo)).To(Equal([]api.TagInfo{{Tag: tagA, Digest: d}, {Tag: "t2", Digest: d}}))
	Expect(c.DeleteManifest(ctx, repo, d)).To(Succeed())
	for _, ti := range tagsOf(ctx, c, repo) {
		Expect(ti.Digest).NotTo(Equal(d), "tag %s still points to %s", ti.Tag, d)
	}
}

// checkPurge pushes tags under prefix and under a sibling prefix that merely
// starts with the same letters, then purges both. Shared by the in-process
// and the zot specs.
func checkPurge(ctx context.Context, c *Client, prefix string) {
	GinkgoHelper()
	for _, ref := range [][2]string{{prefix + "/a", "t1"}, {prefix + "/a", "t2"}, {prefix + "/b/c", "t1"}, {prefix + "2/x", "t1"}} {
		Expect(c.PushSynthetic(ctx, ref[0], ref[1], ref[0], 1)).To(Succeed())
	}
	res, err := c.Purge(ctx, prefix)
	Expect(err).NotTo(HaveOccurred())
	Expect(res.DeletedTags).To(Equal(3), "Purge = %+v", res)
	Expect(res.Repositories).To(Equal([]string{prefix + "/a", prefix + "/b/c"}), "Purge = %+v", res)
	Expect(tagsOf(ctx, c, prefix+"2/x")).To(Equal([]api.TagInfo{{Tag: "t1", Digest: synthDigest(prefix+"2/x", 1)}}), "sibling prefix left alone")
	// a trailing slash means the same prefix
	res, err = c.Purge(ctx, prefix+"2/")
	Expect(err).NotTo(HaveOccurred())
	Expect(res.DeletedTags).To(Equal(1), "Purge(%s2/) = %+v", prefix, res)
}

var _ = Describe("Client", func() {
	Describe("actions", func() {
		It("pushes, tags, deletes tags and manifests, and reports the inventory", func() {
			c, _ := newBackend()
			checkActions(ctx, c, repo)
		})
	})

	Describe("DeleteManifest", func() {
		It("keeps the manifests it was not asked to delete", func() {
			c, _ := newBackend()
			Expect(c.PushSynthetic(ctx, repo, "a", repo, 1)).To(Succeed())
			Expect(c.PushSynthetic(ctx, repo, "b", repo, 2)).To(Succeed())
			Expect(c.DeleteManifest(ctx, repo, synthDigest(repo, 1))).To(Succeed())
			inv, err := c.Inventory(ctx)
			Expect(err).NotTo(HaveOccurred())
			want := []api.Repository{{Name: repo, Tags: []api.TagInfo{{Tag: "b", Digest: synthDigest(repo, 2)}}}}
			Expect(inv).To(Equal(want))
		})

		It("still sends the digest delete when a tag is already gone", func() {
			c, seen := newFlakyBackend(func(r *http.Request) bool {
				return r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/manifests/t1")
			})
			Expect(c.PushSynthetic(ctx, repo, "t1", repo, 1)).To(Succeed())
			Expect(c.DeleteManifest(ctx, repo, synthDigest(repo, 1))).To(Succeed())
			var digestDel bool
			for _, s := range *seen {
				if strings.HasPrefix(s, "DELETE ") && strings.Contains(s, "/manifests/sha256:") {
					digestDel = true
				}
			}
			Expect(digestDel).To(BeTrue(), "digest delete not sent: %v", *seen)
		})
	})

	Describe("error contract", func() {
		DescribeTable("refuses an invalid input with ErrInvalid and sends nothing",
			func(call func(c *Client) error) {
				c, hits := newBackend()
				err := call(c)
				Expect(err).To(MatchError(ErrInvalid))
				Expect(err).NotTo(MatchError(ErrBackend))
				var bad *name.ErrBadName
				Expect(errors.As(err, &bad)).To(BeTrue(), "%v does not reach *name.ErrBadName", err)
				Expect(hits.Load()).To(BeZero(), "backend hit %d times", hits.Load())
			},
			Entry("DeleteTag on a bad repo", func(c *Client) error {
				return c.DeleteTag(ctx, "Bad//repo", "t")
			}),
			Entry("DeleteManifest on a malformed digest", func(c *Client) error {
				return c.DeleteManifest(ctx, repo, "sha256:xyz")
			}),
			Entry("Tag to a tag with a space", func(c *Client) error {
				return c.Tag(ctx, repo, "t", "bad tag")
			}),
			Entry("PushSynthetic on a bad repo", func(c *Client) error {
				return c.PushSynthetic(ctx, "Bad//repo", "t", "k", 1)
			}),
		)

		DescribeTable("reports a backend failure with ErrBackend and keeps the backend error reachable",
			func(status int, fail func(r *http.Request) bool, call func(c *Client) error) {
				var failing atomic.Bool
				c, _ := newStatusBackend(status, func(r *http.Request) bool {
					return failing.Load() && fail(r)
				})
				for _, r := range []string{repo, teamA} {
					Expect(c.PushSynthetic(ctx, r, "t1", r, 1)).To(Succeed())
				}
				failing.Store(true)
				err := call(c)
				Expect(err).To(MatchError(ErrBackend))
				Expect(err).NotTo(MatchError(ErrInvalid))
				var terr *transport.Error
				Expect(errors.As(err, &terr)).To(BeTrue(), "%v does not reach *transport.Error", err)
				Expect(terr.StatusCode).To(Equal(status))
			},
			Entry("DeleteTag when the tag DELETE is refused", http.StatusForbidden,
				deleteOf("/v2/acme/foo/manifests/t1"),
				func(c *Client) error { return c.DeleteTag(ctx, repo, "t1") }),
			Entry("Inventory when the catalog is refused", http.StatusForbidden,
				func(r *http.Request) bool { return r.URL.Path == "/v2/_catalog" },
				func(c *Client) error { _, err := c.Inventory(ctx); return err }),
			Entry("DeleteManifest when the tag DELETE is refused", http.StatusForbidden,
				deleteOf("/v2/acme/foo/manifests/t1"),
				func(c *Client) error { return c.DeleteManifest(ctx, repo, synthDigest(repo, 1)) }),
			Entry("Purge when a tag DELETE is refused", http.StatusForbidden,
				deleteOf("/v2/team/a/manifests/t1"),
				func(c *Client) error { _, err := c.Purge(ctx, "team"); return err }),
		)

		It("reports an unreachable backend with ErrBackend", func() {
			srv := httptest.NewServer(http.NotFoundHandler())
			addr := srv.URL
			srv.Close()
			c, err := New(backendURL(addr))
			Expect(err).NotTo(HaveOccurred())
			err = c.DeleteTag(ctx, repo, "t")
			Expect(err).To(MatchError(ErrBackend))
			Expect(err).NotTo(MatchError(ErrInvalid))
		})
	})

	It("sets the default bounds", func() {
		Expect(NewTransport().ResponseHeaderTimeout).To(Equal(30 * time.Second))
		c, err := New(backendURL("http://zot:5000"))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.actionTimeout).To(Equal(time.Minute))
		Expect(c.listTimeout).To(Equal(5 * time.Minute))
	})

	DescribeTable("ends a call stuck on the backend at its own deadline",
		func(list bool, call func(ctx context.Context, c *Client) error) {
			// The backend holds every request until the client gives up, or
			// 5 s: a wrong bound shows as a long call, not as a hang.
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			DeferCleanup(srv.Close)
			c, err := New(backendURL(srv.URL))
			Expect(err).NotTo(HaveOccurred())
			c.listTimeout, c.actionTimeout = time.Hour, time.Hour
			if list {
				c.listTimeout = 100 * time.Millisecond
			} else {
				c.actionTimeout = 100 * time.Millisecond
			}
			start := time.Now()
			err = call(ctx, c)
			Expect(err).To(MatchError(ErrBackend))
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		},
		Entry("Inventory", true, func(ctx context.Context, c *Client) error {
			_, err := c.Inventory(ctx)
			return err
		}),
		Entry("Purge", true, func(ctx context.Context, c *Client) error {
			_, err := c.Purge(ctx, "acme")
			return err
		}),
		Entry("DeleteTag", false, func(ctx context.Context, c *Client) error {
			return c.DeleteTag(ctx, repo, tagA)
		}),
		Entry("DeleteManifest", false, func(ctx context.Context, c *Client) error {
			return c.DeleteManifest(ctx, repo, synthDigest(repo, 1))
		}),
		Entry("Tag", false, func(ctx context.Context, c *Client) error {
			return c.Tag(ctx, repo, tagA, tagX)
		}),
		Entry("PushSynthetic", false, func(ctx context.Context, c *Client) error {
			return c.PushSynthetic(ctx, repo, tagA, repo, 1)
		}),
	)

	DescribeTable("ends a purge whose tag delete stalls at the earlier deadline",
		func(listTimeout, actionTimeout time.Duration) {
			// DELETE holds until the request ends, or 5 s: a purge that
			// waits for the later deadline shows as a long call.
			h := testutil.NewRegistry()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					h.ServeHTTP(w, r)
					return
				}
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			DeferCleanup(srv.Close)
			c, err := New(backendURL(srv.URL))
			Expect(err).NotTo(HaveOccurred())
			Expect(c.PushSynthetic(ctx, repo, tagA, repo, 1)).To(Succeed())
			c.listTimeout, c.actionTimeout = listTimeout, actionTimeout
			start := time.Now()
			_, err = c.Purge(ctx, "acme")
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		},
		Entry("action 100 ms, list 1 h", time.Hour, 100*time.Millisecond),
		Entry("list 100 ms, action 1 h", 100*time.Millisecond, time.Hour),
	)

	Describe("NewTransport", func() {
		It("never reads the proxy from the environment", func() {
			Expect(NewTransport().Proxy).To(BeNil())
		})
	})

	Describe("New", func() {
		DescribeTable("defaults the port from the scheme",
			func(backend, want string) {
				cl, err := New(backendURL(backend))
				Expect(err).NotTo(HaveOccurred())
				r, err := cl.repo(repo)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.RegistryStr()).To(Equal(want), "%s: RegistryStr()", backend)
			},
			Entry("http without a port gets 80", "http://zot", "zot:80"),
			Entry("https without a port gets 443", "https://zot", "zot:443"),
			Entry("an explicit port is kept", "http://zot:5000", "zot:5000"),
			Entry("an IPv6 host without a port gets 80", "http://[::1]", "[::1]:80"),
		)
	})

	Describe("Inventory", func() {
		It("skips a tag that vanished", func() {
			c, _ := newFlakyBackend(func(r *http.Request) bool {
				return r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/manifests/gone")
			})
			for _, tag := range []string{"gone", "kept"} {
				Expect(c.PushSynthetic(ctx, repo, tag, repo, 1)).To(Succeed())
			}
			inv, err := c.Inventory(ctx)
			Expect(err).NotTo(HaveOccurred())
			want := []api.Repository{{Name: repo, Tags: []api.TagInfo{{Tag: "kept", Digest: synthDigest(repo, 1)}}}}
			Expect(inv).To(Equal(want))
		})

		It("skips a repo that vanished", func() {
			c, _ := newFlakyBackend(func(r *http.Request) bool {
				return strings.HasSuffix(r.URL.Path, "/acme/gone/tags/list")
			})
			for _, repo := range []string{"acme/gone", repo} {
				Expect(c.PushSynthetic(ctx, repo, "t", repo, 1)).To(Succeed())
			}
			inv, err := c.Inventory(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(inv).To(HaveLen(1), "inventory = %+v", inv)
			Expect(inv[0].Name).To(Equal(repo))
		})
	})

	Describe("Purge", func() {
		It("deletes the tags under a prefix only", func() {
			c, _ := newBackend()
			checkPurge(ctx, c, "team")
		})

		It("does not count a tag whose DELETE answers 404", func() {
			var failing atomic.Bool
			c, _ := newFlakyBackend(func(r *http.Request) bool {
				return failing.Load() && r.Method == http.MethodDelete && r.URL.Path == "/v2/team/a/manifests/t2"
			})
			for _, tag := range []string{"t1", "t2"} {
				Expect(c.PushSynthetic(ctx, teamA, tag, teamA, 1)).To(Succeed())
			}
			failing.Store(true)
			res, err := c.Purge(ctx, "team")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(Equal(1), "Purge = %+v, want 1 tag in [team/a]", res)
			Expect(res.Repositories).To(Equal([]string{teamA}), "Purge = %+v", res)
		})

		It("deletes the blobs of the manifests it purges", func() {
			// left behind, the blobs would spare the next copy into the
			// repository its blob reads and uploads (a scenario replay)
			c, _ := newBackend()
			Expect(c.PushSynthetic(ctx, teamA, "t1", teamA, 1)).To(Succeed())
			blobs := blobsOf(ctx, c, teamA, "t1")
			Expect(blobs).NotTo(BeEmpty())
			for _, b := range blobs {
				Expect(blobStatus(ctx, c, teamA, b)).To(Equal(http.StatusOK), "blob %s before the purge", b)
			}
			_, err := c.Purge(ctx, "team")
			Expect(err).NotTo(HaveOccurred())
			for _, b := range blobs {
				Expect(blobStatus(ctx, c, teamA, b)).To(Equal(http.StatusNotFound), "blob %s after the purge", b)
			}
		})

		It("does not fail when the backend refuses blob deletions", func() {
			c, seen := newStatusBackend(http.StatusMethodNotAllowed, func(r *http.Request) bool {
				return r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/blobs/")
			})
			Expect(c.PushSynthetic(ctx, teamA, "t1", teamA, 1)).To(Succeed())
			res, err := c.Purge(ctx, "team")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(Equal(1))
			Expect(strings.Join(*seen, "\n")).To(ContainSubstring("DELETE /v2/team/a/blobs/sha256:"))
		})

		It("purges every repository with PurgeAll", func() {
			c, _ := newBackend()
			for _, ref := range [][2]string{{teamA, "t1"}, {teamA, "t2"}, {"other/x", "t1"}, {"solo", "t1"}} {
				Expect(c.PushSynthetic(ctx, ref[0], ref[1], ref[0], 1)).To(Succeed())
			}
			res, err := c.PurgeAll(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(Equal(4), "PurgeAll = %+v", res)
			Expect(res.Repositories).To(ConsistOf(teamA, "other/x", "solo"))
			for _, r := range []string{teamA, "other/x", "solo"} {
				Expect(tagsOf(ctx, c, r)).To(BeEmpty(), r)
			}
		})

		It("returns zero and an empty non-nil list from PurgeAll on an empty backend", func() {
			c, _ := newBackend()
			res, err := c.PurgeAll(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(BeZero())
			Expect(res.Repositories).NotTo(BeNil())
		})

		It("returns zero and an empty non-nil list when nothing matches", func() {
			c, _ := newBackend()
			res, err := c.Purge(ctx, "nothing/here")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedTags).To(BeZero(), "Purge(no match) = %+v", res)
			Expect(res.Repositories).NotTo(BeNil(), "Purge(no match) = %+v", res)
			Expect(res.Repositories).To(BeEmpty(), "Purge(no match) = %+v", res)
		})

		// An index is removed with its platform manifests, so a client copying the
		// same image again finds none of them and PUTs every manifest again.
		It("deletes the platform manifests of an index", func() {
			c, _ := newBackend()
			Expect(c.PushSynthetic(ctx, "idx/a", "t1", "idx/a", 1)).To(Succeed())
			idx, err := synth.Build("idx/a", 1, nil)
			Expect(err).NotTo(HaveOccurred())
			im, err := idx.IndexManifest()
			Expect(err).NotTo(HaveOccurred())
			Expect(len(im.Manifests)).To(BeNumerically(">=", 2), "synthetic index has %d manifests, want a multi-platform one", len(im.Manifests))
			_, err = c.Purge(ctx, "idx")
			Expect(err).NotTo(HaveOccurred())
			for _, m := range im.Manifests {
				ref, err := c.digestRef("idx/a", m.Digest.String())
				Expect(err).NotTo(HaveOccurred())
				_, err = remote.Head(ref, c.remoteOpts(ctx)...)
				Expect(isNotFound(err)).To(BeTrue(), "platform manifest %s after purge: err=%v, want a 404", m.Digest, err)
			}
		})
	})
})

// blobsOf lists the config and layer digests of every image a tag (an index)
// points to.
func blobsOf(ctx context.Context, c *Client, repo, tag string) []string {
	GinkgoHelper()
	ref, err := c.tagRef(repo, tag)
	Expect(err).NotTo(HaveOccurred())
	idx, err := remote.Index(ref, c.remoteOpts(ctx)...)
	Expect(err).NotTo(HaveOccurred())
	im, err := idx.IndexManifest()
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, d := range im.Manifests {
		img, err := idx.Image(d.Digest)
		Expect(err).NotTo(HaveOccurred())
		m, err := img.Manifest()
		Expect(err).NotTo(HaveOccurred())
		out = append(out, m.Config.Digest.String())
		for _, l := range m.Layers {
			out = append(out, l.Digest.String())
		}
	}
	return out
}

// blobStatus is the status of a HEAD of a blob in a repository.
func blobStatus(ctx context.Context, c *Client, repo, digest string) int {
	GinkgoHelper()
	u := url.URL{Scheme: c.registry.Scheme(), Host: c.registry.RegistryStr(), Path: "/v2/" + repo + "/blobs/" + digest}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	Expect(err).NotTo(HaveOccurred())
	resp, err := c.transport.RoundTrip(req)
	Expect(err).NotTo(HaveOccurred())
	_ = resp.Body.Close()
	return resp.StatusCode
}
