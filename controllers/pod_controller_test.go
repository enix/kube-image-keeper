package controllers

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	dcrenixiov1alpha1 "gitlab.enix.io/products/docker-cache-registry/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var imageMapping = map[string]string{
	"original-init": "test-init",
	"original":      "test",
	"original-2":    "test-2",
}

var podStub = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-pod",
		Namespace: "default",
		Annotations: map[string]string{
			"original-init-image-a": "original-init",
			"original-image-b":      "original",
			"original-image-c":      "original-2",
		},
		Labels: map[string]string{
			"dcr-images-rewritten": "true",
		},
	},
	Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "a", Image: imageMapping["original-init"]},
		},
		Containers: []corev1.Container{
			{Name: "b", Image: imageMapping["original"]},
			{Name: "c", Image: imageMapping["original-2"]},
		},
	},
}

var podStubNotRewritten = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-pod",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "a", Image: "original-init"},
		},
		Containers: []corev1.Container{
			{Name: "b", Image: "original"},
			{Name: "c", Image: "original-2"},
		},
	},
}

func TestDesiredCachedImages(t *testing.T) {
	tests := []struct {
		name         string
		pod          corev1.Pod
		cachedImages []v1alpha1.CachedImage
		wantErr      error
	}{
		{
			name: "basic",
			pod:  podStub,
			cachedImages: []v1alpha1.CachedImage{
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       imageMapping["original"],
					SourceImage: "original",
				}},
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       imageMapping["original-2"],
					SourceImage: "original-2",
				}},
				{Spec: dcrenixiov1alpha1.CachedImageSpec{
					Image:       imageMapping["original-init"],
					SourceImage: "original-init",
				}},
			},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cachedImages, err := desiredCachedImages(context.Background(), &tt.pod)
			if tt.wantErr != nil {
				g.Expect(err).To(Equal(tt.wantErr))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(cachedImages).To(HaveLen(len(tt.cachedImages)))
				for i, cachedImage := range cachedImages {
					g.Expect(cachedImage.Spec.Image).To(Equal(tt.cachedImages[i].Spec.Image))
					g.Expect(cachedImage.Spec.SourceImage).To(Equal(tt.cachedImages[i].Spec.SourceImage))
					g.Expect(cachedImage.Spec.PullSecretsNamespace).To(Equal(tt.pod.Namespace))

					pullSecretNames := []string{}
					for _, pullSecret := range tt.pod.Spec.ImagePullSecrets {
						pullSecretNames = append(pullSecretNames, pullSecret.Name)
					}
					g.Expect(cachedImage.Spec.PullSecretNames).To(ConsistOf(pullSecretNames))

				}
			}
		})
	}
}

var _ = Describe("Pod Controller", func() {

	const timeout = time.Second * 10
	const interval = time.Second * 1

	BeforeEach(func() {
		// Add any setup steps that needs to be executed before each test
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
	})

	Context("Pod with containers and init containers", func() {
		It("Should handle CachedImages creation and deletion", func() {
			By("Creating a pod")
			Expect(k8sClient.Create(context.Background(), &podStub)).Should(Succeed())

			fetched := &dcrenixiov1alpha1.CachedImageList{}
			Eventually(func() []dcrenixiov1alpha1.CachedImage {
				k8sClient.List(context.Background(), fetched)
				return fetched.Items
			}, timeout, interval).Should(HaveLen(len(podStub.Spec.Containers) + len(podStub.Spec.InitContainers)))

			for _, cachedImage := range fetched.Items {
				Expect(imageMapping).To(HaveKey(cachedImage.Spec.SourceImage))
				expectedImage := imageMapping[cachedImage.Spec.SourceImage]
				Expect(cachedImage.Spec.Image).To(Equal(expectedImage))
			}

			By("Deleting previously created pod")
			Expect(k8sClient.Delete(context.Background(), &podStub)).Should(Succeed())

			Eventually(func() []dcrenixiov1alpha1.CachedImage {
				expiringCachedImages := []dcrenixiov1alpha1.CachedImage{}
				k8sClient.List(context.Background(), fetched)
				for _, cachedImage := range fetched.Items {
					if cachedImage.Spec.ExpiresAt != nil {
						expiringCachedImages = append(expiringCachedImages, cachedImage)
					}
				}
				return expiringCachedImages
			}, timeout, interval).Should(HaveLen(len(podStub.Spec.Containers) + len(podStub.Spec.InitContainers)))

			By("Deleting all cached images")
			Expect(k8sClient.DeleteAllOf(context.Background(), &dcrenixiov1alpha1.CachedImage{})).Should(Succeed())
		})
		It("Should not create CachedImages", func() {
			By("Creating a pod without rewriting images")
			Expect(k8sClient.Create(context.Background(), &podStubNotRewritten)).Should(Succeed())

			fetched := &dcrenixiov1alpha1.CachedImageList{}
			Eventually(func() []dcrenixiov1alpha1.CachedImage {
				k8sClient.List(context.Background(), fetched)
				return fetched.Items
			}, timeout, interval).Should(HaveLen(0))

			By("Deleting previously created pod")
			Expect(k8sClient.Delete(context.Background(), &podStubNotRewritten)).Should(Succeed())
		})
	})
})
