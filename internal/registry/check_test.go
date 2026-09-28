package registry

import (
	"context"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

var _ = Describe("Check", func() {
	var client *Client

	BeforeEach(func() {
		client = NewClient()
	})

	Context("on an available image", func() {
		It("sends one manifest HEAD and nothing else", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			reg.Reset()

			_, err := client.Check(ctx, endpoint(reg, "app:v1"))

			Expect(err).NotTo(HaveOccurred())
			manifestRequests := reg.Requests("", "/manifests/")
			Expect(manifestRequests).To(HaveLen(1))
			Expect(manifestRequests[0].Method).To(Equal(http.MethodHead))
			Expect(reg.Requests("", "/blobs/")).To(BeEmpty())
		})

		It("returns the manifest descriptor and the credential that answered", func() {
			reg := newRegistry(registrytest.WithBasicAuth(user, password))
			digest := reg.Push("app:v1", registrytest.Image())
			credential := goodCredential()

			result, err := client.Check(ctx, endpoint(reg, "app:v1", credential))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Descriptor.Digest).To(Equal(digest))
			Expect(result.Auth).To(BeIdenticalTo(credential))
		})
	})

	Context("on a reference pinned by digest, tag included", func() {
		It("sends one manifest HEAD by digest, whatever the tag points at", func() {
			reg := newRegistry()
			pinned := reg.Push("app:v1", registrytest.Image())
			reg.Push("app:v1", registrytest.Image())
			reg.Reset()

			result, err := client.Check(ctx, endpoint(reg, "app:v1@"+pinned.String()))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Descriptor.Digest).To(Equal(pinned))
			manifestRequests := reg.Requests("", "/manifests/")
			Expect(manifestRequests).To(HaveLen(1))
			Expect(manifestRequests[0].Method).To(Equal(http.MethodHead))
			Expect(manifestRequests[0].Path).To(HaveSuffix("/manifests/" + pinned.String()))
		})
	})

	DescribeTable("failure reason",
		func(interceptor registrytest.Interceptor, closed bool, want kuikv1alpha1.CheckFailureReason) {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			reg.Intercept(interceptor)
			image := endpoint(reg, "app:v1")
			if closed {
				image.Reference = registrytest.ClosedHost() + "/app:v1"
			}

			bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, err := client.Check(bounded, image)

			Expect(err).To(HaveOccurred())
			Expect(checkReason(err)).To(Equal(want))
		},
		Entry("is ManifestNotFound on a 404",
			registrytest.Status("", "/manifests/", http.StatusNotFound, nil), false, kuikv1alpha1.CheckManifestNotFound),
		Entry("is Unauthorized on a 401",
			registrytest.Status("", "/manifests/", http.StatusUnauthorized, nil), false, kuikv1alpha1.CheckUnauthorized),
		Entry("is Unauthorized on a 403",
			registrytest.Status("", "/manifests/", http.StatusForbidden, nil), false, kuikv1alpha1.CheckUnauthorized),
		Entry("is QuotaExceeded on a 429 with rate-limit headers",
			registrytest.Status("", "/manifests/", http.StatusTooManyRequests,
				map[string]string{"RateLimit-Remaining": "0;w=21600"}), false, kuikv1alpha1.CheckQuotaExceeded),
		Entry("is QuotaExceeded on a 429 without rate-limit headers",
			registrytest.Status("", "/manifests/", http.StatusTooManyRequests, nil), false, kuikv1alpha1.CheckQuotaExceeded),
		Entry("is QuotaExceeded on a 200 whose rate-limit headers are exhausted",
			registrytest.Interceptor(func(w http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/manifests/") {
					w.Header().Set("RateLimit-Remaining", "0;w=21600")
				}
				return false
			}), false, kuikv1alpha1.CheckQuotaExceeded),
		Entry("is Unreachable when the registry does not answer",
			nil, true, kuikv1alpha1.CheckUnreachable),
		Entry("is Unreachable when the deadline expires",
			registrytest.Interceptor(func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.Contains(r.URL.Path, "/manifests/") {
					return false
				}
				<-r.Context().Done()
				return true
			}), false, kuikv1alpha1.CheckUnreachable),
		Entry("is Unreachable on a 5xx",
			registrytest.Status("", "/manifests/", http.StatusNotImplemented, nil), false, kuikv1alpha1.CheckUnreachable),
	)

	Context("kuik_registry_requests_total", func() {
		It("counts a check on a source registry under its host, operation Check and its result", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())

			_, err := client.Check(ctx, endpoint(reg, "app:v1"))
			Expect(err).NotTo(HaveOccurred())
			_, err = client.Check(ctx, endpoint(reg, "app:missing"))
			Expect(err).To(HaveOccurred())

			Expect(counted(reg.Host(), "Check", "Ok")).To(Equal(1.0))
			Expect(counted(reg.Host(), "Check", string(kuikv1alpha1.CheckManifestNotFound))).To(Equal(1.0))
		})
	})

	Context("on a mirror destination", func() {
		It("reports an outage as Unreachable, never as ManifestNotFound", func() {
			_, err := client.CheckDestination(ctx, Endpoint{Reference: registrytest.ClosedHost() + "/mirror/app:v1", Insecure: true})

			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckUnreachable))
		})

		It("counts nothing in kuik_registry_requests_total", func() {
			reg := newRegistry()
			reg.Push("mirror/app:v1", registrytest.Image())

			_, err := client.CheckDestination(ctx, endpoint(reg, "mirror/app:v1"))
			Expect(err).NotTo(HaveOccurred())
			_, err = client.CheckDestination(ctx, endpoint(reg, "mirror/app:missing"))
			Expect(err).To(HaveOccurred())

			Expect(countedFor(reg.Host())).To(BeZero())
		})
	})
})
