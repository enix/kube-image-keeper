package registry

import (
	"errors"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

// copyTag is the tag the copies of these specs push.
const copyTag = "v1_c"

func copyReason(err error) kuikv1alpha1.CopyFailureReason {
	if copyErr, ok := errors.AsType[*CopyError](err); ok {
		return copyErr.Reason
	}
	return ""
}

var _ = Describe("Copy", func() {
	var (
		client   *Client
		src, dst *registrytest.Registry
	)

	BeforeEach(func() {
		client = NewClient()
		src = newRegistry()
		dst = newRegistry()
	})

	Context("on a multi-platform image", func() {
		It("pushes the upstream index with its digest unchanged", func() {
			digest := src.PushIndex("app:v1", registrytest.Index())

			copied, err := client.Copy(ctx, endpoint(src, "app:v1"), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			Expect(copied).To(Equal(digest))
			descriptor, err := dst.Head("mirror/app:" + copyTag)
			Expect(err).NotTo(HaveOccurred())
			Expect(descriptor.Digest).To(Equal(digest))
			Expect(descriptor.MediaType).To(Equal(types.OCIImageIndex))
		})
	})

	Context("on a single-platform image", func() {
		It("pushes the upstream manifest with its digest unchanged", func() {
			digest := src.Push("app:v1", registrytest.Image())

			copied, err := client.Copy(ctx, endpoint(src, "app:v1"), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			Expect(copied).To(Equal(digest))
			descriptor, err := dst.Head("mirror/app:" + copyTag)
			Expect(err).NotTo(HaveOccurred())
			Expect(descriptor.Digest).To(Equal(digest))
		})
	})

	Context("on a digest-pinned source", func() {
		It("copies the pinned digest even after the upstream tag moved", func() {
			pinned := src.Push("app:v1", registrytest.Image())
			src.Push("app:v1", registrytest.Image())

			copied, err := client.Copy(ctx, endpoint(src, "app:v1@"+pinned.String()), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			Expect(copied).To(Equal(pinned))
			descriptor, err := dst.Head("mirror/app:" + copyTag)
			Expect(err).NotTo(HaveOccurred())
			Expect(descriptor.Digest).To(Equal(pinned))
		})

		It("pushes the manifest under every tag it is given, anchor tag included", func() {
			pinned := src.Push("app:v1", registrytest.Image())
			anchor := "sha256-" + pinned.Hex + "_c"

			_, err := client.Copy(ctx, endpoint(src, "app@"+pinned.String()), endpoint(dst, "mirror/app"), []string{copyTag, anchor})

			Expect(err).NotTo(HaveOccurred())
			for _, tag := range []string{copyTag, anchor} {
				descriptor, err := dst.Head("mirror/app:" + tag)
				Expect(err).NotTo(HaveOccurred())
				Expect(descriptor.Digest).To(Equal(pinned))
			}
		})
	})

	Context("when the destination repository already holds the digest", func() {
		It("pushes the tags without uploading any blob", func() {
			img := registrytest.Image()
			digest := src.Push("app:v1", img)
			dst.Push("mirror/app:v1_other", img)
			dst.Reset()

			_, err := client.Copy(ctx, endpoint(src, "app:v1"), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			Expect(dst.Requests("", "/blobs/")).To(BeEmpty())
			descriptor, err := dst.Head("mirror/app:" + copyTag)
			Expect(err).NotTo(HaveOccurred())
			Expect(descriptor.Digest).To(Equal(digest))
		})
	})

	Context("with credentials on both sides", func() {
		It("reads the source with its own credentials and writes the destination with its own", func() {
			src = newRegistry(registrytest.WithBasicAuth("source", "source-password"))
			dst = newRegistry(registrytest.WithBasicAuth("mirror", "mirror-password"))
			digest := src.Push("app:v1", registrytest.Image())

			_, err := client.Copy(ctx,
				endpoint(src, "app:v1", &authn.Basic{Username: "source", Password: "source-password"}),
				endpoint(dst, "mirror/app", &authn.Basic{Username: "mirror", Password: "mirror-password"}),
				[]string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			descriptor, err := dst.Head("mirror/app:" + copyTag)
			Expect(err).NotTo(HaveOccurred())
			Expect(descriptor.Digest).To(Equal(digest))
		})
	})

	DescribeTable("failure reason",
		func(setup func() (Endpoint, Endpoint), want kuikv1alpha1.CopyFailureReason) {
			source, destination := setup()

			_, err := client.Copy(ctx, source, destination, []string{copyTag})

			Expect(err).To(HaveOccurred())
			Expect(copyReason(err)).To(Equal(want))
		},
		Entry("is SourceNotFound on a 404 from the source", func() (Endpoint, Endpoint) {
			return endpoint(src, "app:missing"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopySourceNotFound),
		Entry("is SourceUnreachable when the source does not answer", func() (Endpoint, Endpoint) {
			return Endpoint{Reference: registrytest.ClosedHost() + "/app:v1", Insecure: true}, endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopySourceUnreachable),
		Entry("is DestinationUnreachable when the destination does not answer", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			return endpoint(src, "app:v1"), Endpoint{Reference: registrytest.ClosedHost() + "/mirror/app", Insecure: true}
		}, kuikv1alpha1.CopyDestinationUnreachable),
		Entry("is PushRejected when the destination refuses the manifest", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			dst.Intercept(registrytest.Status(http.MethodPut, "/manifests/", http.StatusBadRequest, nil))
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopyPushRejected),
		Entry("is Unauthorized on a 401 from the source", func() (Endpoint, Endpoint) {
			src = newRegistry(registrytest.WithBasicAuth(user, password))
			src.Push("app:v1", registrytest.Image())
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopyUnauthorized),
		Entry("is Unauthorized on a 401 from the destination", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			dst = newRegistry(registrytest.WithBasicAuth(user, password))
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopyUnauthorized),
		Entry("is QuotaExceeded on a 429 from the source", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			src.Intercept(registrytest.Status("", "/manifests/", http.StatusTooManyRequests, nil))
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopyQuotaExceeded),
		Entry("is SourceUnreachable on a 5xx from the source", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			src.Intercept(registrytest.Status("", "/manifests/", http.StatusNotImplemented, nil))
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopySourceUnreachable),
		Entry("is DestinationUnreachable on a 5xx from the destination", func() (Endpoint, Endpoint) {
			src.Push("app:v1", registrytest.Image())
			dst.Intercept(registrytest.Status(http.MethodPut, "/manifests/", http.StatusNotImplemented, nil))
			return endpoint(src, "app:v1"), endpoint(dst, "mirror/app")
		}, kuikv1alpha1.CopyDestinationUnreachable),
	)

	Context("kuik_registry_requests_total", func() {
		It("counts the source read under operation Copy and nothing for the destination", func() {
			src.Push("app:v1", registrytest.Image())

			_, err := client.Copy(ctx, endpoint(src, "app:v1"), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(err).NotTo(HaveOccurred())
			Expect(counted(src.Host(), "Copy", "Ok")).To(Equal(1.0))
			Expect(countedFor(dst.Host())).To(BeZero())
		})

		It("counts a source 404 as ManifestNotFound, the side-agnostic reason", func() {
			_, err := client.Copy(ctx, endpoint(src, "app:missing"), endpoint(dst, "mirror/app"), []string{copyTag})

			Expect(copyReason(err)).To(Equal(kuikv1alpha1.CopySourceNotFound))
			Expect(counted(src.Host(), "Copy", string(kuikv1alpha1.CheckManifestNotFound))).To(Equal(1.0))
		})
	})
})
