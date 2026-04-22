package kuik

import (
	"context"
	"slices"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These tests guard the behavior fixed in commit d26a099: when the webhook has
// rewritten a pod's image (original A -> current B), the mirror controllers
// (ImageSetMirror / ClusterImageSetMirror) must match their filters against
// B (the current image actually in the spec), not A (the original image
// stored in the kuik.enix.io/original-images annotation).
var _ = Describe("Mirror controllers match on rewritten, not original image", func() {
	const (
		originalImage  = "docker.io/library/nginx:1.25"
		rewrittenImage = "rewritten.example.com/library/nginx:1.25"
	)

	ctx := context.Background()

	newRewrittenPod := func() corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rewritten-pod",
				Namespace: "default",
				Annotations: map[string]string{
					OriginalImagesAnnotation: `{"app":"` + originalImage + `"}`,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: rewrittenImage},
				},
			},
		}
	}

	Context("normalizedImageNamesFromPod (used by the mirror reconcilers)", func() {
		It("returns only the rewritten container image", func() {
			pod := newRewrittenPod()
			got := slices.Collect(normalizedImageNamesFromPod(&pod))
			Expect(got).To(ConsistOf(rewrittenImage))
			Expect(got).NotTo(ContainElement(originalImage))
		})
	})

	Context("normalizedImageNamesFromAnnotatedPod (used by the availability reconciler)", func() {
		It("returns both the current and the original image, so availability is tracked for both", func() {
			pod := newRewrittenPod()
			got := slices.Collect(normalizedImageNamesFromAnnotatedPod(ctx, &pod))
			Expect(got).To(ConsistOf(rewrittenImage, originalImage))
		})
	})

	Context("podsByNormalizedMatchingImages (mirror matching logic)", func() {
		It("matches the pod on the rewritten image when the filter includes it", func() {
			f := kuikv1alpha1.ImageFilterDefinition{
				Include: []string{`rewritten\.example\.com/.*`},
			}.MustBuild()

			pods := []corev1.Pod{newRewrittenPod()}
			got := podsByNormalizedMatchingImages(ctx, f, nil, pods)
			Expect(got).To(HaveKey(rewrittenImage))
			Expect(got).NotTo(HaveKey(originalImage))
		})

		It("does not match the pod when only the original (pre-rewrite) image matches the filter", func() {
			f := kuikv1alpha1.ImageFilterDefinition{
				Include: []string{`docker\.io/library/nginx:.*`},
			}.MustBuild()

			pods := []corev1.Pod{newRewrittenPod()}
			got := podsByNormalizedMatchingImages(ctx, f, nil, pods)
			Expect(got).To(BeEmpty())
		})
	})
})
