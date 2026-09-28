package registry

import (
	"context"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

var _ = Describe("Client", func() {
	var client *Client

	BeforeEach(func() {
		client = NewClient()
	})

	Context("with several credentials", func() {
		var reg *registrytest.Registry

		BeforeEach(func() {
			reg = newRegistry(registrytest.WithBasicAuth(user, password))
			reg.Push("app:v1", registrytest.Image())
			reg.Reset()
		})

		It("tries them in order and stops at the first that answers", func() {
			good := goodCredential()
			result, err := client.Check(ctx, endpoint(reg, "app:v1", badCredential(), good, goodCredential()))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Auth).To(BeIdenticalTo(good))
			Expect(reg.Requests(http.MethodHead, "/manifests/")).To(HaveLen(2))
		})

		It("does not fall back to anonymous when every credential is refused", func() {
			_, err := client.Check(ctx, endpoint(reg, "app:v1", badCredential(), badCredential()))

			Expect(err).To(HaveOccurred())
			manifestRequests := reg.Requests("", "/manifests/")
			Expect(manifestRequests).To(HaveLen(2))
			for _, req := range manifestRequests {
				Expect(req.Authorized).To(BeTrue())
			}
		})
	})

	Context("with no credential", func() {
		It("reads anonymously", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())
			reg.Reset()

			_, err := client.Check(ctx, endpoint(reg, "app:v1"))

			Expect(err).NotTo(HaveOccurred())
			manifestRequests := reg.Requests("", "/manifests/")
			Expect(manifestRequests).To(HaveLen(1))
			Expect(manifestRequests[0].Authorized).To(BeFalse())
		})
	})

	Context("with an insecure registry", func() {
		It("reaches it over plain HTTP", func() {
			reg := newRegistry()
			reg.Push("app:v1", registrytest.Image())

			_, err := client.Check(ctx, Endpoint{Reference: reg.Host() + "/app:v1", Insecure: true})

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the registry answers 429", func() {
		It("sends the request once, without retrying it", func() {
			reg := newRegistry()
			reg.Intercept(registrytest.Status("", "/manifests/", http.StatusTooManyRequests,
				map[string]string{"RateLimit-Remaining": "0;w=21600"}))

			_, err := client.Check(ctx, endpoint(reg, "app:v1"))

			Expect(checkReason(err)).To(Equal(kuikv1alpha1.CheckQuotaExceeded))
			Expect(reg.Requests("", "/manifests/")).To(HaveLen(1))
		})
	})

	Context("with a deadline on the caller's context", func() {
		It("bounds the whole operation, every credential attempt included", func() {
			const attempt = 400 * time.Millisecond
			reg := newRegistry()
			reg.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
				select {
				case <-time.After(attempt):
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusUnauthorized)
				return true
			})

			bounded, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := client.Check(bounded, endpoint(reg, "app:v1", badCredential(), badCredential(), badCredential()))

			Expect(err).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*attempt))
		})
	})

	Context("with a zero timeout", func() {
		It("leaves the operation unbounded", func() {
			unbounded, cancel := WithTimeout(ctx, 0)
			defer cancel()

			_, hasDeadline := unbounded.Deadline()
			Expect(hasDeadline).To(BeFalse())
		})
	})

	Context("when concurrent calls share one client", func() {
		It("reads the rate-limit headers of each call from its own response", func() {
			limited := newRegistry()
			limited.Push("app:v1", registrytest.Image())
			limited.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("RateLimit-Remaining", "0;w=21600")
				return false
			})
			healthy := newRegistry()
			healthy.Push("app:v1", registrytest.Image())

			const calls = 20
			var wg sync.WaitGroup
			limitedErrs := make([]error, calls)
			healthyErrs := make([]error, calls)
			for i := range calls {
				wg.Add(2)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, limitedErrs[i] = client.Check(ctx, endpoint(limited, "app:v1"))
				}()
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, healthyErrs[i] = client.Check(ctx, endpoint(healthy, "app:v1"))
				}()
			}
			wg.Wait()

			for i := range calls {
				Expect(checkReason(limitedErrs[i])).To(Equal(kuikv1alpha1.CheckQuotaExceeded))
				Expect(healthyErrs[i]).NotTo(HaveOccurred())
			}
		})
	})
})
