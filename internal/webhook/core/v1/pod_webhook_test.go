package v1

import (
	"context"
	"net"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/filter"
	"github.com/maypok86/otter"
	"go4.org/syncutil/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newSkipCheckTestDefaulter builds a PodCustomDefaulter with the in-memory
// dependencies (cache + singleflight + default config) needed to exercise
// checkImageAvailabilityCached. Tests that need custom timeouts can mutate
// d.Config.Routing.ActiveCheck.Timeout after construction.
func newSkipCheckTestDefaulter() *PodCustomDefaulter {
	GinkgoHelper()
	checkCache, err := otter.MustBuilder[string, bool](1000).WithTTL(1 * time.Minute).Build()
	Expect(err).NotTo(HaveOccurred())
	testConfig, err := config.LoadDefault()
	Expect(err).NotTo(HaveOccurred())
	return &PodCustomDefaulter{
		Config:       testConfig,
		checkCache:   checkCache,
		requestGroup: &singleflight.Group{},
	}
}

// reserveUnboundLoopbackAddr binds an ephemeral loopback port, frees it, and
// returns the "127.0.0.1:<port>" string. Subsequent TCP dials to that address
// receive a RST from the kernel — i.e. fail fast with "connection refused",
// without depending on assumptions about reserved low ports.
func reserveUnboundLoopbackAddr() string {
	GinkgoHelper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := listener.Addr().String()
	Expect(listener.Close()).To(Succeed())
	return addr
}

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

var _ = Describe("global skipLabels / skipAnnotations", func() {
	const skipLabel = "kube-image-keeper.enix.io/image-caching-policy"

	buildDefaulter := func(skipLabels, skipAnnotations []string) *PodCustomDefaulter {
		f, err := filter.CompilePodFilter(nil, skipLabels, nil, skipAnnotations)
		Expect(err).NotTo(HaveOccurred())
		return &PodCustomDefaulter{globalPodFilter: *f}
	}

	It("leaves a pod untouched when it matches the global skip label", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "skip-me",
				Labels:    map[string]string{skipLabel: "ignore"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "docker.io/library/nginx:1.29"},
				},
			},
		}

		d := buildDefaulter([]string{skipLabel + "=ignore"}, nil)
		Expect(d.defaultPod(context.Background(), pod, true)).To(Succeed())

		By("leaving the container image untouched")
		Expect(pod.Spec.Containers[0].Image).To(Equal("docker.io/library/nginx:1.29"))

		By("not writing the original-images annotation")
		Expect(pod.Annotations).NotTo(HaveKey("kuik.enix.io/original-images"))

		By("not injecting any imagePullSecrets")
		Expect(pod.Spec.ImagePullSecrets).To(BeEmpty())
	})

	It("leaves a pod untouched when it matches a global skip annotation", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        "skip-by-annotation",
				Annotations: map[string]string{"meta.helm.sh/release-namespace": "my-namespace"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "docker.io/library/nginx:1.29"},
				},
			},
		}

		d := buildDefaulter(nil, []string{"meta.helm.sh/release-namespace=my-namespace"})
		Expect(d.defaultPod(context.Background(), pod, true)).To(Succeed())

		Expect(pod.Spec.Containers[0].Image).To(Equal("docker.io/library/nginx:1.29"))
		Expect(pod.Annotations).NotTo(HaveKey("kuik.enix.io/original-images"))
		Expect(pod.Spec.ImagePullSecrets).To(BeEmpty())
	})

	It("does not short-circuit a pod that lacks the skip label", func() {
		// Start with no annotations and a digest-pinned container.
		// If the global skip fires, defaultPod returns immediately and
		// pod.Annotations stays nil. If it doesn't fire, defaultPod
		// initializes the map, writes the original-images annotation, then
		// drops the digest container at the per-container filter and exits
		// cleanly without ever calling the (nil) client. Asserting the
		// annotation was written distinguishes the two paths.
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "regular-pod",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "docker.io/library/nginx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
				},
			},
		}

		d := buildDefaulter([]string{skipLabel + "=ignore"}, nil)
		Expect(d.defaultPod(context.Background(), pod, true)).To(Succeed())

		By("writing the original-images annotation (proves defaultPod proceeded past the global skip)")
		Expect(pod.Annotations).To(HaveKey("kuik.enix.io/original-images"))
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

var _ = Describe("buildAlternativesList", func() {
	var d *PodCustomDefaulter

	BeforeEach(func() {
		testConfig, err := config.LoadDefault()
		Expect(err).NotTo(HaveOccurred())
		d = &PodCustomDefaulter{Client: k8sClient, Config: testConfig}
	})

	references := func(c *Container) []string {
		refs := make([]string, len(c.Images))
		for i, img := range c.Images {
			refs[i] = img.Reference
		}
		return refs
	}

	makeContainer := func(image string, policy corev1.PullPolicy) *Container {
		return &Container{
			Container: &corev1.Container{
				Name:            "app",
				Image:           image,
				ImagePullPolicy: policy,
			},
			NormalizedImage: image,
			Alternatives:    map[string]struct{}{},
		}
	}

	makeRIS := func(upstreams ...kuikv1alpha1.ReplicatedUpstream) kuikv1alpha1.ReplicatedImageSet {
		return kuikv1alpha1.ReplicatedImageSet{
			Spec: kuikv1alpha1.ReplicatedImageSetSpec{
				Upstreams: upstreams,
			},
		}
	}

	makeUpstream := func(registry string, discard bool) kuikv1alpha1.ReplicatedUpstream {
		return kuikv1alpha1.ReplicatedUpstream{
			ImageReference: kuikv1alpha1.ImageReference{Registry: registry},
			ImageFilter: kuikv1alpha1.ImageFilterDefinition{
				Include: []string{".*"},
			},
			DiscardAlternative: discard,
		}
	}

	cismWithMirror := func(priority int, registry, mirrorPath string) kuikv1alpha1.ImageSetMirror {
		return kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "global"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				Priority: priority,
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: registry, Path: mirrorPath},
				},
			},
		}
	}

	Context("with imagePullPolicy: Always", func() {
		It("pins the original first by default, ignoring negative spec.priority", func() {
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullAlways)
			err := d.buildAlternativesList(
				ctx,
				[]kuikv1alpha1.ImageSetMirror{cismWithMirror(-1, "harbor.example.com", "/mirror")},
				nil,
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"docker.io/library/nginx:1.29",
				"harbor.example.com/mirror/library/nginx:1.29",
			}))
		})

		It("honors a CISM with negative spec.priority when HonorPrioritiesOnAlwaysImagePullPolicy is true (issue #561)", func() {
			d.Config.Routing.HonorPrioritiesOnAlwaysImagePullPolicy = true
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullAlways)
			err := d.buildAlternativesList(
				ctx,
				[]kuikv1alpha1.ImageSetMirror{cismWithMirror(-1, "harbor.example.com", "/mirror")},
				nil,
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"harbor.example.com/mirror/library/nginx:1.29",
				"docker.io/library/nginx:1.29",
			}))
		})

		It("places original first when HonorPrioritiesOnAlwaysImagePullPolicy is true and CISM priority is 0", func() {
			d.Config.Routing.HonorPrioritiesOnAlwaysImagePullPolicy = true
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullAlways)
			err := d.buildAlternativesList(
				ctx,
				[]kuikv1alpha1.ImageSetMirror{cismWithMirror(0, "harbor.example.com", "/mirror")},
				nil,
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"docker.io/library/nginx:1.29",
				"harbor.example.com/mirror/library/nginx:1.29",
			}))
		})
	})

	Context("with discardAlternative", func() {
		It("excludes a discarded upstream from the alternatives list", func() {
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(
				ctx,
				nil,
				[]kuikv1alpha1.ReplicatedImageSet{
					makeRIS(makeUpstream("docker.io", true)),
				},
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{}))
		})

		ris := []kuikv1alpha1.ReplicatedImageSet{
			makeRIS(
				makeUpstream("docker.io", true),
				makeUpstream("mirror.example.com", false),
			),
		}

		It("excludes a discarded upstream while keeping the active one", func() {
			c := makeContainer("mirror.example.com/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(ctx, nil, ris, c)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"mirror.example.com/library/nginx:1.29",
			}))
		})

		It("matches a upstream that have been discarded without routing it", func() {
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(ctx, nil, ris, c)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"mirror.example.com/library/nginx:1.29",
			}))
		})

		It("keep the original image when the discarded upstream is not the one that it matches", func() {
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(
				ctx,
				nil,
				[]kuikv1alpha1.ReplicatedImageSet{
					makeRIS(
						makeUpstream("mirror1.example.com", true),
						makeUpstream("mirror2.example.com", false),
						makeUpstream("docker.io", false),
					),
				},
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"docker.io/library/nginx:1.29",
				"mirror2.example.com/library/nginx:1.29",
			}))
		})
	})

	Context("with imagePullPolicy: IfNotPresent", func() {
		It("honors a CISM with negative spec.priority", func() {
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(
				ctx,
				[]kuikv1alpha1.ImageSetMirror{cismWithMirror(-1, "harbor.example.com", "/mirror")},
				nil,
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"harbor.example.com/mirror/library/nginx:1.29",
				"docker.io/library/nginx:1.29",
			}))
		})

		It("is unaffected by HonorPrioritiesOnAlwaysImagePullPolicy", func() {
			d.Config.Routing.HonorPrioritiesOnAlwaysImagePullPolicy = true
			c := makeContainer("docker.io/library/nginx:1.29", corev1.PullIfNotPresent)
			err := d.buildAlternativesList(
				ctx,
				[]kuikv1alpha1.ImageSetMirror{cismWithMirror(-1, "harbor.example.com", "/mirror")},
				nil,
				c,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(references(c)).To(Equal([]string{
				"harbor.example.com/mirror/library/nginx:1.29",
				"docker.io/library/nginx:1.29",
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

var _ = Describe("effectiveSkipCheck", func() {
	It("should default to false when both values are nil", func() {
		Expect(effectiveSkipCheck(nil, nil)).To(BeFalse())
	})

	It("should use CR-level value when per-item is nil", func() {
		trueVal := true
		falseVal := false
		Expect(effectiveSkipCheck(nil, &trueVal)).To(BeTrue())
		Expect(effectiveSkipCheck(nil, &falseVal)).To(BeFalse())
	})

	It("should use per-item value when CR-level is nil", func() {
		trueVal := true
		falseVal := false
		Expect(effectiveSkipCheck(&trueVal, nil)).To(BeTrue())
		Expect(effectiveSkipCheck(&falseVal, nil)).To(BeFalse())
	})

	It("should prefer per-item value over CR-level value", func() {
		truePerItem := true
		falseCR := false
		Expect(effectiveSkipCheck(&truePerItem, &falseCR)).To(BeTrue())

		falsePerItem := false
		trueCR := true
		Expect(effectiveSkipCheck(&falsePerItem, &trueCR)).To(BeFalse())
	})

	It("should handle all true values", func() {
		truePerItem := true
		trueCR := true
		Expect(effectiveSkipCheck(&truePerItem, &trueCR)).To(BeTrue())
	})
})

var _ = Describe("checkImageAvailabilityCached with skipActiveCheck", func() {
	It("should skip probe when skipActiveCheck is true", func() {
		d := newSkipCheckTestDefaulter()
		image := &AlternativeImage{
			Reference:       "localhost:5000/cache/library/nginx:latest",
			SkipActiveCheck: true,
		}
		Expect(d.checkImageAvailabilityCached(context.Background(), image, nil)).NotTo(HaveOccurred())
	})

	It("should return cached success when skipActiveCheck is false", func() {
		d := newSkipCheckTestDefaulter()
		image := &AlternativeImage{
			Reference:       "docker.io/library/nginx:latest",
			SkipActiveCheck: false,
		}
		d.checkCache.Set(image.Reference, true)
		Expect(d.checkImageAvailabilityCached(context.Background(), image, nil)).NotTo(HaveOccurred())
	})

	It("should return cached failure when skipActiveCheck is false", func() {
		d := newSkipCheckTestDefaulter()
		image := &AlternativeImage{
			Reference:       "docker.io/library/nginx:latest",
			SkipActiveCheck: false,
		}
		d.checkCache.Set(image.Reference, false)
		err := d.checkImageAvailabilityCached(context.Background(), image, nil)
		Expect(err).To(MatchError("cached"))
	})

	It("should skip cache when skipActiveCheck is true", func() {
		d := newSkipCheckTestDefaulter()
		image := &AlternativeImage{
			Reference:       "localhost:5000/cache/library/nginx:latest",
			SkipActiveCheck: true,
		}
		// Even with a cached failure, skip should return nil
		d.checkCache.Set(image.Reference, false)
		Expect(d.checkImageAvailabilityCached(context.Background(), image, nil)).NotTo(HaveOccurred())
	})

	// Closes the loop: this exercises a probe that would actually fail (the
	// reference points to a closed port), proving SkipActiveCheck bypasses the
	// real network call — not just the cache.
	It("should bypass a real failing probe when SkipActiveCheck is true", func() {
		// Reserve and release an ephemeral loopback port: any subsequent TCP
		// connect to it gets RST = "connection refused", without depending on
		// assumptions about reserved low ports (e.g. tcpmux on port 1).
		unreachable := reserveUnboundLoopbackAddr() + "/library/nginx:latest"

		d := newSkipCheckTestDefaulter()
		// Tighten the timeout so the failing-probe case fails fast.
		d.Config.Routing.ActiveCheck.Timeout = 200 * time.Millisecond

		By("returning an error when SkipActiveCheck is false")
		probed := &AlternativeImage{Reference: unreachable, SkipActiveCheck: false}
		Expect(d.checkImageAvailabilityCached(context.Background(), probed, nil)).To(HaveOccurred())

		By("returning nil when SkipActiveCheck is true on the same unreachable reference")
		// Use a fresh PodCustomDefaulter so the prior negative cache entry
		// doesn't influence the result — we want to prove the network call
		// itself is skipped.
		d2 := newSkipCheckTestDefaulter()
		d2.Config.Routing.ActiveCheck.Timeout = 200 * time.Millisecond
		trusted := &AlternativeImage{Reference: unreachable, SkipActiveCheck: true}
		Expect(d2.checkImageAvailabilityCached(context.Background(), trusted, nil)).NotTo(HaveOccurred())
	})
})

var _ = Describe("buildAlternativesList with skipActiveCheck", func() {
	var d *PodCustomDefaulter

	BeforeEach(func() {
		testConfig, err := config.LoadDefault()
		Expect(err).NotTo(HaveOccurred())
		d = &PodCustomDefaulter{Client: k8sClient, Config: testConfig}
	})

	makeContainer := func(image string) *Container {
		return &Container{
			Container: &corev1.Container{
				Name:  "app",
				Image: image,
			},
			NormalizedImage: image,
			Alternatives:    map[string]struct{}{},
		}
	}

	// findByPrefix returns the alternative whose Reference matches the given
	// registry+path prefix. c.Images always contains the original image as
	// well, so we cannot key tests on positional index. The prefix match is
	// boundary-aware: the next character after the prefix must be a path or
	// tag separator (`/`, `:`, `@`) — otherwise `harbor.example.com/mirror`
	// would silently match a future `harbor.example.com/mirror-v2` entry.
	findByPrefix := func(c *Container, prefix string) AlternativeImage {
		GinkgoHelper()
		for _, img := range c.Images {
			rest, ok := strings.CutPrefix(img.Reference, prefix)
			if !ok {
				continue
			}
			if rest == "" || rest[0] == '/' || rest[0] == ':' || rest[0] == '@' {
				return img
			}
		}
		Fail("no alternative starting with " + prefix + " in container.Images")
		return AlternativeImage{}
	}

	It("should set SkipActiveCheck to false when neither CR nor mirror has skipActiveCheck", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		ism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "harbor.example.com", Path: "/mirror"},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{ism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "harbor.example.com/mirror").SkipActiveCheck).To(BeFalse())
	})

	// Guard: skipActiveCheck must only apply to mirrors/upstreams, never to
	// the original image. buildAlternativesList constructs the original entry
	// without going through effectiveSkipCheck (see pod_webhook.go where the
	// `original` prioritizedAlternative is built); this test pins that
	// invariant so a future refactor cannot silently propagate the flag to
	// the original image.
	It("should never set SkipActiveCheck on the original image even when CR has skipActiveCheck=true", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueVal := true
		ism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				SkipActiveCheck: &trueVal,
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "harbor.example.com", Path: "/mirror"},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{ism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "docker.io/library/nginx").SkipActiveCheck).To(BeFalse())
		// Sanity check: the mirror is still flagged as skipped.
		Expect(findByPrefix(c, "harbor.example.com/mirror").SkipActiveCheck).To(BeTrue())
	})

	It("should use CR-level skipActiveCheck when mirror doesn't override", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueVal := true
		ism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				SkipActiveCheck: &trueVal,
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "harbor.example.com", Path: "/mirror"},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{ism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "harbor.example.com/mirror").SkipActiveCheck).To(BeTrue())
	})

	It("should use mirror-level skipActiveCheck when it overrides CR-level", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueCR := true
		falseMirror := false
		ism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				SkipActiveCheck: &trueCR,
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{
						Registry:        "harbor.example.com",
						Path:            "/mirror",
						SkipActiveCheck: &falseMirror,
					},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{ism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "harbor.example.com/mirror").SkipActiveCheck).To(BeFalse())
	})

	It("should handle mixed mirrors with different skipActiveCheck values", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueMirror1 := true
		falseMirror2 := false
		ism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{
						Registry:        "localhost",
						Path:            "/fast-cache",
						SkipActiveCheck: &trueMirror1,
						Priority:        1,
					},
					{
						Registry:        "fallback.example.com",
						Path:            "/backup",
						SkipActiveCheck: &falseMirror2,
						Priority:        10,
					},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{ism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "localhost/fast-cache").SkipActiveCheck).To(BeTrue())
		Expect(findByPrefix(c, "fallback.example.com/backup").SkipActiveCheck).To(BeFalse())
	})

	// ClusterImageSetMirror is normalized to ImageSetMirror by the caller of
	// buildAlternativesList (see pod_webhook.go where cismList items are copied
	// into the ImageSetMirror slice). This test exercises the normalized form
	// produced from a cluster-scoped CR.
	It("should handle skipActiveCheck on a CR originating from ClusterImageSetMirror", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueVal := true
		cism := kuikv1alpha1.ImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cism"},
			Spec: kuikv1alpha1.ImageSetMirrorSpec{
				SkipActiveCheck: &trueVal,
				ImageFilter: kuikv1alpha1.ImageFilterDefinition{
					Include: []string{".*"},
				},
				Mirrors: kuikv1alpha1.Mirrors{
					{Registry: "localhost", Path: "/kuik-cache", Priority: 1},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), []kuikv1alpha1.ImageSetMirror{cism}, nil, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "localhost/kuik-cache").SkipActiveCheck).To(BeTrue())
	})

	// Same normalization story for ClusterReplicatedImageSet. We need a first
	// upstream that matches the source image (docker.io) so the mirror upstream
	// is added to the alternatives list.
	It("should handle skipActiveCheck on a CR originating from ClusterReplicatedImageSet", func() {
		c := makeContainer("docker.io/library/nginx:latest")
		trueVal := true
		cris := kuikv1alpha1.ReplicatedImageSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cris"},
			Spec: kuikv1alpha1.ReplicatedImageSetSpec{
				SkipActiveCheck: &trueVal,
				Upstreams: []kuikv1alpha1.ReplicatedUpstream{
					{
						ImageReference: kuikv1alpha1.ImageReference{Registry: "docker.io", Path: ""},
						ImageFilter:    kuikv1alpha1.ImageFilterDefinition{Include: []string{".*"}},
					},
					{
						ImageReference: kuikv1alpha1.ImageReference{Registry: "localhost", Path: "/kuik-cache"},
						ImageFilter:    kuikv1alpha1.ImageFilterDefinition{Include: []string{".*"}},
						Priority:       1,
					},
				},
			},
		}

		err := d.buildAlternativesList(context.Background(), nil, []kuikv1alpha1.ReplicatedImageSet{cris}, c)
		Expect(err).NotTo(HaveOccurred())
		Expect(findByPrefix(c, "localhost/kuik-cache").SkipActiveCheck).To(BeTrue())
	})

})
