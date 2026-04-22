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

// Regression tests for https://github.com/enix/kube-image-keeper/issues/567.
//
// Scenario: two registries, A (upstream) and B (mirror). After an image is
// mirrored to B and becomes unavailable on A, the webhook rewrites new pods'
// container.Image from A/... to B/.... Those pods still carry the original
// A/... reference in the kuik.enix.io/original-images annotation.
//
// The mirror reconcilers must still treat the original A/... image as "in
// use" for as long as such a rewritten pod exists; otherwise updateUnusedSince
// marks the image unused and the cleanup loop eventually deletes it from B,
// breaking new pulls.
var _ = Describe("Issue #567: rewritten pods keep the original image in use", func() {
	const (
		originalImage  = "a.example.com/test/foo:v1"
		rewrittenImage = "b.example.com/test/foo:v1"
		mirrorPrefix   = "b.example.com"
	)

	ctx := context.Background()

	newRewrittenPod := func(name string) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
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

	Context("podsInUseImages (the decoupled in-use signal)", func() {
		It("returns the original image for a rewritten pod, so existing mirror entries stay alive", func() {
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			got := podsInUseImages(ctx, pods)

			// Both names must appear; updateUnusedSince keys off the original
			// reference stored in the annotation to avoid marking the mirrored
			// image as unused while the rewritten pod still needs it.
			Expect(got).To(HaveKey(originalImage))
			Expect(got).To(HaveKey(rewrittenImage))
		})
	})

	Context("mergePreviousAndCurrentMatchingImages", func() {
		It("leaves unusedSince nil for the original image while a rewritten pod still references it", func() {
			spec := &kuikv1alpha1.ImageSetMirrorSpec{
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{`a\.example\.com/.*`},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "b.example.com"},
				},
			}

			// Prior reconciliation (when pod-1 was still around, unrewritten)
			// already recorded the original image in the status.
			status := &kuikv1alpha1.ImageSetMirrorStatus{
				MatchingImages: []kuikv1alpha1.MatchingImage{
					{
						Image: originalImage,
						Mirrors: []kuikv1alpha1.MirrorStatus{
							{Image: rewrittenImage},
						},
					},
				},
			}

			// pod-1 (unrewritten) is gone; only pod-2 (rewritten to B) remains.
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			mirrorPrefixes := map[string][]string{"": {mirrorPrefix}}

			_, err := mergePreviousAndCurrentMatchingImages(ctx, pods, spec, status, mirrorPrefixes)
			Expect(err).NotTo(HaveOccurred())

			Expect(status.MatchingImages).To(HaveLen(1))
			Expect(status.MatchingImages[0].Image).To(Equal(originalImage))
			Expect(status.MatchingImages[0].UnusedSince).To(BeNil(),
				"unusedSince must stay nil while a rewritten pod still needs the mirrored image")
		})

		It("does not add the original image to matchingImages on a fresh CISM (mirroring is driven by the current image only)", func() {
			// The two concerns are decoupled: rewritten pods must keep an
			// EXISTING mirror entry alive (covered above), but must NOT create
			// new mirror entries for the original reference — only the current
			// container image drives what gets mirrored (d26a099).
			spec := &kuikv1alpha1.ImageSetMirrorSpec{
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{`a\.example\.com/.*`},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "b.example.com"},
				},
			}
			status := &kuikv1alpha1.ImageSetMirrorStatus{}
			pods := []corev1.Pod{newRewrittenPod("pod-2")}
			mirrorPrefixes := map[string][]string{"": {mirrorPrefix}}

			_, err := mergePreviousAndCurrentMatchingImages(ctx, pods, spec, status, mirrorPrefixes)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.MatchingImages).To(BeEmpty())
		})
	})
})

// These tests guard the behavior fixed in commit d26a099: when the webhook
// has rewritten a pod's image (original A -> current B), the mirror
// reconcilers must match their filters against B (the current image actually
// in the spec), not A (the original image stored in the
// kuik.enix.io/original-images annotation). The "still in use" signal is
// decoupled and covered separately (see issue #567 regression tests).
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

	Context("normalizedImageNamesFromAnnotatedPod (used by the availability reconciler and the in-use signal)", func() {
		It("returns both the current and the original image", func() {
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
