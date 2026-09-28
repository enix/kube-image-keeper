package registry

import (
	"errors"
	"net/http"

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
