package core

import (
	"context"
	"testing"
	"time"

	kuikv1alpha1 "github.com/adisplayname/kube-image-keeper/api/kuik/v1alpha1ext1"
	"github.com/adisplayname/kube-image-keeper/internal/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var podStub = corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-pod",
		Namespace: "default",
		Annotations: map[string]string{
			registry.ContainerAnnotationKey("a", true):  "alpine",
			registry.ContainerAnnotationKey("b", false): "nginx",
			registry.ContainerAnnotationKey("c", false): "busybox",
		},
		Labels: map[string]string{
			LabelManagedName: "true",
		},
	},
	Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "a", Image: "alpine:3.14"},
		},
		Containers: []corev1.Container{
			{Name: "b", Image: "nginx:1.22"},
			{Name: "c", Image: "busybox:1.35"},
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
			{Name: "a", Image: "alpine"},
		},
		Containers: []corev1.Container{
			{Name: "b", Image: "nginx"},
			{Name: "c", Image: "busybox"},
		},
	},
}

func TestDesiredCachedImages(t *testing.T) {
	tests := []struct {
		name         string
		pod          corev1.Pod
		cachedImages []kuikv1alpha1.CachedImage
	}{
		{
			name: "basic",
			pod:  podStub,
			cachedImages: []kuikv1alpha1.CachedImage{
				{Spec: kuikv1alpha1.CachedImageSpec{
					SourceImage: "nginx",
				}},
				{Spec: kuikv1alpha1.CachedImageSpec{
					SourceImage: "busybox",
				}},
				{Spec: kuikv1alpha1.CachedImageSpec{
					SourceImage: "alpine",
				}},
			},
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cachedImages := DesiredCachedImages(context.Background(), &tt.pod)
			g.Expect(cachedImages).To(HaveLen(len(tt.cachedImages)))
			for i, cachedImage := range cachedImages {
				g.Expect(cachedImage.Spec.SourceImage).To(Equal(tt.cachedImages[i].Spec.SourceImage))
			}
		})
	}
}

func Test_cachedImageFromSourceImage(t *testing.T) {
	tests := []struct {
		name               string
		sourceImage        string
		expectedRepository string
		expectedName       string
	}{
		{
			name:               "basic",
			sourceImage:        "alpine",
			expectedRepository: "docker.io-library-alpine",
			expectedName:       "docker.io-library-alpine-latest",
		},
		{
			name:               "with latest tag",
			sourceImage:        "alpine:latest",
			expectedRepository: "docker.io-library-alpine",
			expectedName:       "docker.io-library-alpine-latest",
		},
		{
			name:               "with another tag",
			sourceImage:        "alpine:3.16.3",
			expectedRepository: "docker.io-library-alpine",
			expectedName:       "docker.io-library-alpine-3.16.3",
		},
	}

	g := NewWithT(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cachedImage, err := cachedImageFromSourceImage(tt.sourceImage)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cachedImage.Name).To(Equal(tt.expectedName))
			g.Expect(cachedImage.Spec.SourceImage).To(Equal(tt.sourceImage))
			g.Expect(cachedImage.Spec.ExpiresAt).To(BeNil())
		})
	}
}

var _ = Describe("Pod Controller", func() {

	const timeout = time.Second * 30
	const interval = time.Second * 1

	BeforeEach(func() {
	})

	AfterEach(func() {
		suceedOrNotFound := Or(Succeed(), MatchError(ContainSubstring("not found")))
		Expect(k8sClient.Delete(context.Background(), &podStub)).Should(suceedOrNotFound)
		Expect(k8sClient.Delete(context.Background(), &podStubNotRewritten)).Should(suceedOrNotFound)

		// allow to reuse the stub
		podStub.ResourceVersion = ""
		podStubNotRewritten.ResourceVersion = ""

		By("Deleting all cached images")
		Expect(k8sClient.DeleteAllOf(context.Background(), &kuikv1alpha1.CachedImage{})).Should(Succeed())
	})

	Context("Pod with containers and init containers", func() {
		It("Should handle CachedImages creation and deletion", func() {
			By("Creating a pod")
			Expect(k8sClient.Create(context.Background(), &podStub)).Should(Succeed())

			fetched := &kuikv1alpha1.CachedImageList{}
			Eventually(func() []kuikv1alpha1.CachedImage {
				_ = k8sClient.List(context.Background(), fetched)
				return fetched.Items
			}, timeout, interval).Should(HaveLen(len(podStub.Spec.Containers) + len(podStub.Spec.InitContainers)))

			annotationsImages := []string{}
			for _, annotation := range podStub.ObjectMeta.Annotations {
				annotationsImages = append(annotationsImages, annotation)
			}
			cachedImages := []string{}
			for _, cachedImage := range fetched.Items {
				cachedImages = append(cachedImages, cachedImage.Spec.SourceImage)
			}
			Expect(cachedImages).To(ConsistOf(annotationsImages))

			By("Deleting previously created pod")
			Expect(k8sClient.Delete(context.Background(), &podStub)).Should(Succeed())
		})
		It("Should not create CachedImages", func() {
			By("Creating a pod without rewriting images")
			Expect(k8sClient.Create(context.Background(), &podStubNotRewritten)).Should(Succeed())

			fetched := &kuikv1alpha1.CachedImageList{}
			Eventually(func() []kuikv1alpha1.CachedImage {
				_ = k8sClient.List(context.Background(), fetched)
				return fetched.Items
			}, timeout, interval).Should(HaveLen(0))
		})
	})
})
