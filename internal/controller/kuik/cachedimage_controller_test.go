package kuik

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1ext1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1ext1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("CachedImage Controller", func() {

	const timeout = time.Second * 30
	const interval = time.Second * 1

	Context("When creating CachedImages", func() {
		It("Should expire image that are not retained only", func() {
			fetched := &kuikv1alpha1ext1.CachedImageList{}

			By("Creating an image without the retain flag", func() {
				Expect(k8sClient.Create(context.Background(), &kuikv1alpha1ext1.CachedImage{
					ObjectMeta: v1.ObjectMeta{
						Name: "nginx",
					},
					Spec: kuikv1alpha1ext1.CachedImageSpec{
						SourceImage: "nginx",
					},
				})).Should(Succeed())

				Eventually(func() []kuikv1alpha1ext1.CachedImage {
					expiringCachedImages := []kuikv1alpha1ext1.CachedImage{}
					_ = k8sClient.List(context.Background(), fetched)
					for _, cachedImage := range fetched.Items {
						if cachedImage.Spec.ExpiresAt != nil {
							expiringCachedImages = append(expiringCachedImages, cachedImage)
						}
					}
					return expiringCachedImages
				}, timeout, interval).Should(HaveLen(1))
			})

			By("Creating an expiring image with the retain flag", func() {
				Expect(k8sClient.Create(context.Background(), &kuikv1alpha1ext1.CachedImage{
					ObjectMeta: v1.ObjectMeta{
						Name: "alpine",
					},
					Spec: kuikv1alpha1ext1.CachedImageSpec{
						SourceImage: "alpine",
						Retain:      true,
						ExpiresAt:   &v1.Time{Time: time.Now().Add(time.Hour)},
					},
				})).Should(Succeed())

				Eventually(func() []kuikv1alpha1ext1.CachedImage {
					expiringCachedImages := []kuikv1alpha1ext1.CachedImage{}
					_ = k8sClient.List(context.Background(), fetched)
					for _, cachedImage := range fetched.Items {
						if cachedImage.Spec.ExpiresAt != nil {
							expiringCachedImages = append(expiringCachedImages, cachedImage)
						}
					}
					return expiringCachedImages
				}, timeout, interval).Should(HaveLen(1))
			})

		})
	})
})
