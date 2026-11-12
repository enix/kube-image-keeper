//go:build e2e

package fakeregistry

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testerconfig"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/testutil"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// zotProxies points the proxies mirror and nodelete at the zot backend.
func zotProxies() serverOption {
	GinkgoHelper()
	zot, ok := testutil.ZotURL()
	if !ok {
		Skip("ZOT_URL is not set")
	}
	return withProxies(testerconfig.ProxyHost{Name: mirror, Backend: zot}, testerconfig.ProxyHost{Name: noDelete, Backend: zot})
}

func reqlogFilter(repo string) reqlog.Filter { return reqlog.Filter{Repo: repo} }

var _ = Describe("Proxy mirror in front of zot", func() {
	It("forwards a push, a pull, a tag list and a delete", func() {
		srv, _, log := newProxyServer(nil, zotProxies())
		pushPullDelete(srv, log, testutil.UniqueRepo("zot-push-pull-delete"))
	})

	It("mounts a blob across repos instead of uploading it again", func() {
		srv, _, log := newProxyServer(nil, zotProxies())
		opts := testutil.Opts(srv)
		base := testutil.UniqueRepo("zot-cross-repo-mount")
		a, b := base+"-a", base+"-b"
		idx, err := synth.Build(a, 1, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.WriteIndex(testutil.Ref(mirrorFQDN+"/"+a+":t1"), idx, opts...)).To(Succeed())
		rem, err := remote.Index(testutil.Ref(mirrorFQDN+"/"+a+":t1"), opts...)
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.WriteIndex(testutil.Ref(mirrorFQDN+"/"+b+":t1"), rem, opts...)).To(Succeed())
		var mounted bool
		for _, e := range log.Query(reqlogFilter(b)) {
			Expect(e.Kind == api.KindUpload && e.Method == http.MethodPatch).To(BeFalse(), "PATCH upload for repo %s: %+v", b, e)
			if e.Kind == api.KindUpload && e.Method == http.MethodPost && e.Status == 201 {
				mounted = true
			}
		}
		Expect(mounted).To(BeTrue(), "no mounted upload (POST answered 201) for the second repo")
	})

	// A paginated tags/list must not leak the backend address through Link.
	It("does not name the backend in the Link header of a paginated tags list", func() {
		srv, _, _ := newProxyServer(nil, zotProxies())
		opts := testutil.Opts(srv)
		repo := testutil.UniqueRepo("zot-tags-list-link")
		idx, err := synth.Build(repo, 1, nil)
		Expect(err).NotTo(HaveOccurred())
		for _, tag := range []string{"t1", "t2", "t3"} {
			Expect(remote.WriteIndex(testutil.Ref(mirrorFQDN+"/"+repo+":"+tag), idx, opts...)).To(Succeed())
		}
		resp, body := raw(srv, http.MethodGet, mirrorFQDN, "/v2/"+repo+"/tags/list?n=1", nil)
		Expect(resp.StatusCode).To(Equal(200), "tags/list?n=1: %s", body)
		link := resp.Header.Get("Link")
		GinkgoWriter.Printf("Link: %q\n", link)
		Expect(link).NotTo(BeEmpty(), "zot sent no Link header for a paginated tags/list; the check below would be empty")
		// Each <URL> in Link must be relative or name the public host, never the backend.
		var urls []string
		for rest := link; ; {
			i := strings.Index(rest, "<")
			if i < 0 || !strings.Contains(rest[i:], ">") {
				break
			}
			j := i + strings.Index(rest[i:], ">")
			urls = append(urls, rest[i+1:j])
			rest = rest[j+1:]
		}
		Expect(urls).NotTo(BeEmpty(), "Link %q has no <URL>", link)
		for _, raw := range urls {
			u, err := url.Parse(raw)
			Expect(err).NotTo(HaveOccurred())
			if u.Host != "" {
				Expect(u.Host).To(Equal(mirrorFQDN), "Link URL %q names the backend host", raw)
			}
		}
	})
})
