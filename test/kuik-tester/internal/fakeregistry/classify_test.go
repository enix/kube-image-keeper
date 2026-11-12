package fakeregistry

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("Classify", func() {
	var (
		dg   = "sha256:" + strings.Repeat("ab", 32)
		long = strings.Repeat("a", 128)
	)

	DescribeTable("parses a request line",
		func(method, path string, want Parsed, ok bool) {
			got, gotOK := Classify(method, path)
			Expect(gotOK).To(Equal(ok), "Classify(%s %s) = %+v", method, path, got)
			Expect(got).To(Equal(want), "Classify(%s %s)", method, path)
		},
		Entry("ping with a trailing slash", http.MethodGet, "/v2/", Parsed{Kind: api.KindPing}, true),
		Entry("ping without a trailing slash", http.MethodGet, "/v2", Parsed{Kind: api.KindPing}, true),
		Entry("catalog", http.MethodGet, "/v2/_catalog", Parsed{Kind: api.KindCatalog}, true),
		Entry("token", http.MethodGet, "/token", Parsed{Kind: api.KindToken}, true),
		Entry("HEAD manifest by tag", http.MethodHead, manifest1, Parsed{Kind: api.KindManifestByTag, Repo: repo, Tag: "t1"}, true),
		Entry("GET manifest by tag", http.MethodGet, manifest1, Parsed{Kind: api.KindManifestByTag, Repo: repo, Tag: "t1"}, true),
		Entry("GET manifest by digest", http.MethodGet, "/v2/acme/foo/manifests/"+dg, Parsed{Kind: api.KindManifestByDigest, Repo: repo, Digest: dg}, true),
		Entry("PUT manifest", http.MethodPut, manifest1, Parsed{Kind: api.KindManifestPut, Repo: repo, Tag: "t1"}, true),
		Entry("DELETE manifest by tag", http.MethodDelete, manifest1, Parsed{Kind: api.KindDelete, Repo: repo, Tag: "t1"}, true),
		Entry("DELETE manifest by digest", http.MethodDelete, "/v2/acme/foo/manifests/"+dg, Parsed{Kind: api.KindDelete, Repo: repo, Digest: dg}, true),
		Entry("GET blob", http.MethodGet, "/v2/acme/foo/blobs/"+dg, Parsed{Kind: api.KindBlob, Repo: repo, Digest: dg}, true),
		Entry("HEAD blob", http.MethodHead, "/v2/acme/foo/blobs/"+dg, Parsed{Kind: api.KindBlob, Repo: repo, Digest: dg}, true),
		Entry("DELETE blob", http.MethodDelete, "/v2/acme/foo/blobs/"+dg, Parsed{Kind: api.KindDelete, Repo: repo, Digest: dg}, true),
		Entry("POST upload start", http.MethodPost, "/v2/acme/foo/blobs/uploads/", Parsed{Kind: api.KindUpload, Repo: repo}, true),
		Entry("PATCH upload chunk", http.MethodPatch, "/v2/acme/foo/blobs/uploads/3f2a-uuid", Parsed{Kind: api.KindUpload, Repo: repo}, true),
		Entry("PUT upload end", http.MethodPut, "/v2/acme/foo/blobs/uploads/3f2a-uuid", Parsed{Kind: api.KindUpload, Repo: repo}, true),
		Entry("tags list", http.MethodGet, "/v2/acme/foo/tags/list", Parsed{Kind: api.KindTagsList, Repo: repo}, true),
		Entry("deep repo path", http.MethodGet, "/v2/team_x/app.v2/deep/er/path/manifests/t1", Parsed{Kind: api.KindManifestByTag, Repo: "team_x/app.v2/deep/er/path", Tag: "t1"}, true),
		Entry("128-character tag", http.MethodGet, "/v2/acme/foo/manifests/"+long, Parsed{Kind: api.KindManifestByTag, Repo: repo, Tag: long}, true),
		Entry("tag with an underscore and a dash", http.MethodGet, "/v2/acme/foo/manifests/t1_cluster-a", Parsed{Kind: api.KindManifestByTag, Repo: repo, Tag: tagCluster}, true),
		Entry("repo named manifests/x", http.MethodGet, "/v2/manifests/x/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: "manifests/x", Tag: "t"}, true),
		Entry("129-character tag is refused", http.MethodGet, "/v2/acme/foo/manifests/"+long+"a", Parsed{}, false),
		Entry("malformed digest is refused", http.MethodGet, "/v2/acme/foo/manifests/sha256:zz", Parsed{}, false),
		Entry("manifest without a repo is refused", http.MethodGet, "/v2/manifests/t1", Parsed{}, false),
		Entry("repo without an endpoint is refused", http.MethodGet, "/v2/acme/foo", Parsed{}, false),
		Entry("dot-dot segment in a manifest path is refused", http.MethodGet, "/v2/public/../secret/foo/manifests/t", Parsed{}, false),
		Entry("dot segment is refused", http.MethodGet, "/v2/./a/manifests/t", Parsed{}, false),
		Entry("dot-dot segment in a blob path is refused", http.MethodGet, "/v2/a/../blobs/"+dg, Parsed{}, false),
		Entry("dots inside a segment are allowed", http.MethodGet, "/v2/a.b/c.d/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: "a.b/c.d", Tag: "t"}, true),
		Entry("a double dot inside a segment is refused", http.MethodGet, "/v2/a.b/c..d/manifests/t", Parsed{}, false),
		Entry("an upper-case repo is refused", http.MethodGet, "/v2/Acme/foo/manifests/t", Parsed{}, false),
		Entry("a quote in a repo is refused", http.MethodGet, `/v2/a",realm="x/manifests/t`, Parsed{}, false),
		Entry("a 256-byte repo is refused", http.MethodGet, "/v2/"+strings.Repeat("a", 256)+"/manifests/t", Parsed{}, false),
		Entry("a 255-byte repo is accepted", http.MethodGet, "/v2/"+strings.Repeat("a", 255)+"/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: strings.Repeat("a", 255), Tag: "t"}, true),
		Entry("a 33-segment repo is refused", http.MethodGet, "/v2/"+strings.Repeat("a/", 32)+"a/manifests/t", Parsed{}, false),
		Entry("a 32-segment repo is accepted", http.MethodGet, "/v2/"+strings.Repeat("a/", 31)+"a/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: strings.Repeat("a/", 31) + "a", Tag: "t"}, true),
		Entry("a leading separator is refused", http.MethodGet, "/v2/-a/manifests/t", Parsed{}, false),
		Entry("a trailing separator is refused", http.MethodGet, "/v2/a-/manifests/t", Parsed{}, false),
		Entry("an upload session named dot-dot is refused", http.MethodPost, "/v2/a/blobs/uploads/..", Parsed{}, false),
		Entry("an upload session named dot is refused", http.MethodPost, "/v2/a/blobs/uploads/.", Parsed{}, false),
		Entry("separators inside segments are accepted", http.MethodGet, "/v2/a__b/c---d/e_f.g/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: "a__b/c---d/e_f.g", Tag: "t"}, true),
		Entry("a kuik mirror path is accepted", http.MethodGet, "/v2/mir02/upstream.kt.test/acme/mir02/app/manifests/t", Parsed{Kind: api.KindManifestByTag, Repo: "mir02/upstream.kt.test/acme/mir02/app", Tag: "t"}, true),
		Entry("unknown API version is refused", http.MethodGet, "/v3/", Parsed{}, false),
		Entry("root is refused", http.MethodGet, "/", Parsed{}, false),
	)
})
