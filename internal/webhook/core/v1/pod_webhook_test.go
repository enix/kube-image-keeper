package v1

import (
	"context"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Pod Webhook", func() {
	var (
		obj       *corev1.Pod
		oldObj    *corev1.Pod
		defaulter PodCustomDefaulter
	)

	BeforeEach(func() {
		obj = &corev1.Pod{}
		oldObj = &corev1.Pod{}
		defaulter = PodCustomDefaulter{}
		Expect(defaulter).NotTo(BeNil(), "Expected defaulter to be initialized")
		Expect(oldObj).NotTo(BeNil(), "Expected oldObj to be initialized")
		Expect(obj).NotTo(BeNil(), "Expected obj to be initialized")
		// TODO (user): Add any setup logic common to all tests
	})

	AfterEach(func() {
		// TODO (user): Add any teardown logic common to all tests
	})

	Context("When creating Pod under Defaulting Webhook", func() {
		// TODO (user): Add logic for defaulting webhooks
		// Example:
		// It("Should apply defaults when a required field is empty", func() {
		//     By("simulating a scenario where defaults should be applied")
		//     obj.SomeFieldWithDefault = ""
		//     By("calling the Default method to apply defaults")
		//     defaulter.Default(ctx, obj)
		//     By("checking that the default values are set")
		//     Expect(obj.SomeFieldWithDefault).To(Equal("default_value"))
		// })
	})

	Context("When the pod is a kubelet mirror pod (static pod)", func() {
		It("should skip mutation entirely (no image rewrite, no imagePullSecrets injection, no annotation written)", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-system",
					Name:      "kube-apiserver-node1",
					Annotations: map[string]string{
						"kubernetes.io/config.mirror": "abc123",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "kube-apiserver", Image: "registry.k8s.io/kube-apiserver:v1.30.0"},
					},
				},
			}

			d := &PodCustomDefaulter{}
			Expect(d.defaultPod(context.Background(), pod, true)).To(Succeed())

			By("leaving the container image untouched")
			Expect(pod.Spec.Containers[0].Image).To(Equal("registry.k8s.io/kube-apiserver:v1.30.0"))

			By("not injecting any imagePullSecrets")
			Expect(pod.Spec.ImagePullSecrets).To(BeEmpty())

			By("not writing the original-images annotation")
			Expect(pod.Annotations).NotTo(HaveKey("kuik.enix.io/original-images"))
		})

		It("should not short-circuit a regular pod that lacks the mirror annotation", func() {
			// Seed OriginalImagesAnnotation so defaultPod exits at the
			// "all containers already processed" branch without needing
			// a real client to list mirror/replicated CRs.
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "regular-pod",
					Annotations: map[string]string{
						"kuik.enix.io/original-images": `{"app":"docker.io/library/nginx:1.27"}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "docker.io/library/nginx:1.27"},
					},
				},
			}

			d := &PodCustomDefaulter{}
			Expect(d.defaultPod(context.Background(), pod, true)).To(Succeed())

			By("preserving the original-images annotation (proves the mirror guard did not fire)")
			Expect(pod.Annotations).To(HaveKeyWithValue("kuik.enix.io/original-images", `{"app":"docker.io/library/nginx:1.27"}`))
		})
	})
})

var _ = Describe("compareAlternatives", func() {
	refs := func(alternatives []prioritizedAlternative) []string {
		result := make([]string, len(alternatives))
		for i, alt := range alternatives {
			result[i] = alt.reference
		}
		return result
	}

	Context("default behavior (all priorities 0)", func() {
		It("should preserve default type order: CISM < ISM < CRIS < RIS", func() {
			alternatives := []prioritizedAlternative{
				{reference: "ris-mirror", typeOrder: crTypeOrderRIS, declarationOrder: 0},
				{reference: "cism-mirror", typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "cris-upstream", typeOrder: crTypeOrderCRIS, declarationOrder: 0},
				{reference: "ism-mirror", typeOrder: crTypeOrderISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"cism-mirror", "ism-mirror", "cris-upstream", "ris-mirror",
			}))
		})

		It("should preserve YAML declaration order within same type", func() {
			alternatives := []prioritizedAlternative{
				{reference: "mirror-c", typeOrder: crTypeOrderCISM, declarationOrder: 2},
				{reference: "mirror-a", typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "mirror-b", typeOrder: crTypeOrderCISM, declarationOrder: 1},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"mirror-a", "mirror-b", "mirror-c",
			}))
		})
	})

	Context("CR priority", func() {
		It("should sort by CR priority ascending (negative first)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-0", crPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-5", crPriority: 5, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-neg10", crPriority: -10, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-neg1", crPriority: -1, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"prio-neg10", "prio-neg1", "prio-0", "prio-5",
			}))
		})

		It("should fall back to type order on equal CR priority", func() {
			alternatives := []prioritizedAlternative{
				{reference: "ism", crPriority: -1, typeOrder: crTypeOrderISM, declarationOrder: 0},
				{reference: "cism", crPriority: -1, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"cism", "ism",
			}))
		})
	})

	Context("intra-CR priority", func() {
		It("should sort positive intra-priorities ascending (lower = higher priority)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-30", intraPriority: 30, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-10", intraPriority: 10, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "prio-20", intraPriority: 20, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"prio-10", "prio-20", "prio-30",
			}))
		})

		It("should place intra-priority 0 (default) before positive values", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-5", intraPriority: 5, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-1", intraPriority: 1, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "no-prio", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"no-prio", "prio-1", "prio-5",
			}))
		})

		It("should preserve YAML order among items with intra-priority 0", func() {
			alternatives := []prioritizedAlternative{
				{reference: "first", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "second", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "third", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})
	})

	Context("combined scenario from instructions.txt", func() {
		It("should order: ISM mirrors (prio -10) > CISM mirror (prio -1) > original (prio 0)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "second", crPriority: -10, intraPriority: 5, typeOrder: crTypeOrderISM, declarationOrder: 0},
				{reference: "first", crPriority: -10, intraPriority: 1, typeOrder: crTypeOrderISM, declarationOrder: 1},
				{reference: "third", crPriority: -1, intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})

		It("should order ReplicatedImageSet upstreams by intra-priority", func() {
			alternatives := []prioritizedAlternative{
				{reference: "third", crPriority: 0, intraPriority: 30, typeOrder: crTypeOrderCRIS, declarationOrder: 0},
				{reference: "first", crPriority: 0, intraPriority: 10, typeOrder: crTypeOrderCRIS, declarationOrder: 1},
				{reference: "second", crPriority: 0, intraPriority: 20, typeOrder: crTypeOrderCRIS, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})
	})
})

var _ = Describe("clearStaleMirrorStatus", func() {
	It("should respect context cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		d := &PodCustomDefaulter{Client: k8sClient}
		image := &AlternativeImage{
			Reference: "mirror.example.com/cache/library/nginx:latest",
			SecretOwner: &kuikv1alpha1.ImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ism", Namespace: "default"},
				Status: kuikv1alpha1.ImageSetMirrorStatus{
					MatchingImages: []kuikv1alpha1.MatchingImage{
						{
							Image: "docker.io/library/nginx:latest",
							Mirrors: []kuikv1alpha1.MirrorStatus{
								{Image: "mirror.example.com/cache/library/nginx:latest", MirroredAt: &metav1.Time{Time: time.Now()}},
							},
						},
					},
				},
			},
		}

		d.clearStaleMirrorStatus(ctx, image)
		// Should return quickly without hanging (context is already cancelled)
	})

	It("should drop cleanup when semaphore is full", func() {
		sem := make(chan struct{}, 1)
		// Fill the semaphore
		sem <- struct{}{}

		image := &AlternativeImage{
			Reference:   "mirror.example.com/cache/library/nginx:latest",
			SecretOwner: &kuikv1alpha1.ImageSetMirror{},
		}

		d := &PodCustomDefaulter{
			Client: k8sClient,
			Config: &config.Config{
				Routing: config.Routing{
					ActiveCheck: config.ActiveCheck{
						StaleMirrorCleanup: config.StaleMirrorCleanup{
							Timeout: time.Second,
						},
					},
				},
			},
			cleanupSemaphore: sem,
		}

		By("Returning nil when the semaphore is full")
		Expect(d.tryCleanupStaleMirrorStatus(context.Background(), image)).To(BeNil())

		By("Freeing the semaphore")
		<-sem

		By("Launching and completing the cleanup")
		done := d.tryCleanupStaleMirrorStatus(context.Background(), image)
		Expect(done).NotTo(BeNil())
		Eventually(done, 2*time.Second).Should(BeClosed())
	})
})
