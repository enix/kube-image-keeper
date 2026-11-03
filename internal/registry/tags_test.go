package registry

import (
	"errors"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

var _ = Describe("ListTags", func() {
	var client *Client

	BeforeEach(func() {
		client = NewClient()
	})

	It("lists every tag of the repository, other clusters' included", func() {
		reg := newRegistry()
		img := registrytest.Image()
		for _, tag := range []string{"v1_a", "v1_b", "v2_a"} {
			reg.Push("mirror/app:"+tag, img)
		}

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/app"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(ConsistOf("v1_a", "v1_b", "v2_a"))
	})

	It("returns no tag for a repository the registry does not know", func() {
		reg := newRegistry()

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/unknown"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(BeEmpty())
	})

	It("lists every page of a paginated tags/list before returning", func() {
		reg := newRegistry(registrytest.WithTagsPageSize(2))
		img := registrytest.Image()
		want := []string{"t1", "t2", "t3", "t4", "t5"}
		for _, tag := range want {
			reg.Push("mirror/app:"+tag, img)
		}

		tags, err := client.ListTags(ctx, endpoint(reg, "mirror/app"))

		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(ConsistOf(want))
		Expect(len(reg.Requests(http.MethodGet, "/tags/list"))).To(BeNumerically(">=", 3))
	})
})

var _ = Describe("DeleteTag", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("deletes the tag and leaves the other tags of the same manifest", func() {
		img := registrytest.Image()
		reg.Push("mirror/app:v1_a", img)
		reg.Push("mirror/app:v1_b", img)

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))).To(Succeed())

		_, err := reg.Head("mirror/app:v1_a")
		Expect(err).To(HaveOccurred())
		_, err = reg.Head("mirror/app:v1_b")
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a digest reference", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app@"+digest.String()))).NotTo(Succeed())

		Expect(reg.Requests(http.MethodDelete, "")).To(BeEmpty())
		_, err := reg.Head("mirror/app:v1_a")
		Expect(err).NotTo(HaveOccurred())
	})

	It("succeeds when the tag is already gone", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_gone"))).To(Succeed())
	})

	It("returns ErrDeleteUnsupported when the registry answers 405", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(registrytest.Status(http.MethodDelete, "/manifests/", http.StatusMethodNotAllowed, nil))

		err := client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(errors.Is(err, ErrDeleteUnsupported)).To(BeTrue())
	})

	It("counts nothing in kuik_registry_requests_total", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteTag(ctx, endpoint(reg, "mirror/app:v1_a"))).To(Succeed())

		Expect(countedFor(reg.Host())).To(BeZero())
	})
})

var _ = Describe("ManifestDigest", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("returns the digest of the manifest a tag points at", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
	})

	It("returns the digest of the index a multi-platform tag points at", func() {
		digest := reg.PushIndex("mirror/app:v1_a", registrytest.Index())

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
	})

	It("falls back to a GET when the registry answers the HEAD without Docker-Content-Digest", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())
		reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method != http.MethodHead || !strings.Contains(r.URL.Path, "/manifests/") {
				return false
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(http.StatusOK)
			return true
		})

		got, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(digest))
		Expect(reg.Requests(http.MethodGet, "/manifests/v1_a")).NotTo(BeEmpty())
	})

	It("reports a tag the registry does not hold as not found, with no error", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		_, found, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_gone"))

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	It("counts nothing in kuik_registry_requests_total", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		_, _, err := client.ManifestDigest(ctx, endpoint(reg, "mirror/app:v1_a"))

		Expect(err).NotTo(HaveOccurred())
		Expect(countedFor(reg.Host())).To(BeZero())
	})
})

var _ = Describe("DeleteManifest", func() {
	var (
		client *Client
		reg    *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		reg = newRegistry()
	})

	It("deletes the manifest of a digest reference", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(reg.Requests(http.MethodDelete, "/manifests/"+digest.String())).To(HaveLen(1))
		_, err := reg.Head("mirror/app@" + digest.String())
		Expect(err).To(HaveOccurred())
	})

	It("refuses a tag reference, sending no DELETE", func() {
		reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app:v1_a"))).NotTo(Succeed())

		Expect(reg.Requests(http.MethodDelete, "")).To(BeEmpty())
	})

	It("succeeds when the manifest is already gone", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())
		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())
	})

	DescribeTable("returns ErrManifestDeleteUnsupported when the registry refuses manifest deletion",
		func(code int) {
			digest := reg.Push("mirror/app:v1_a", registrytest.Image())
			reg.Intercept(registrytest.Status(http.MethodDelete, "/manifests/", code, nil))

			err := client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))

			Expect(errors.Is(err, ErrManifestDeleteUnsupported)).To(BeTrue())
		},
		Entry("400 Bad Request", http.StatusBadRequest),
		Entry("405 Method Not Allowed", http.StatusMethodNotAllowed),
		Entry("501 Not Implemented", http.StatusNotImplemented),
	)

	It("counts nothing in kuik_registry_requests_total", func() {
		digest := reg.Push("mirror/app:v1_a", registrytest.Image())

		Expect(client.DeleteManifest(ctx, endpoint(reg, "mirror/app@"+digest.String()))).To(Succeed())

		Expect(countedFor(reg.Host())).To(BeZero())
	})
})
