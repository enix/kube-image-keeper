package kuik

import (
	"context"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("ClusterImageSetMirror Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		clusterimagesetmirror := &kuikv1alpha1.ClusterImageSetMirror{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ClusterImageSetMirror")
			err := k8sClient.Get(ctx, typeNamespacedName, clusterimagesetmirror)
			if err != nil && errors.IsNotFound(err) {
				resource := &kuikv1alpha1.ClusterImageSetMirror{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterImageSetMirror")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ClusterImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When an image is no longer used by any pod", func() {
		const resourceName = "test-unused-since"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}

		newReconciler := func() *ClusterImageSetMirrorReconciler {
			return &ClusterImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				},
			}
		}

		BeforeEach(func() {
			By("Creating a CISM with a filter and mirror")
			resource := &kuikv1alpha1.ClusterImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
					ImageFilter: kuikv1alpha1.ImageFilterDefinition{
						Include: []string{"docker\\.io/library/nginx:.*"},
					},
					Mirrors: kuikv1alpha1.Mirrors{
						{Registry: "mirror.example.com", Path: "cache"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Seeding status with a matching image that has no UnusedSince")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			resource.Status.MatchingImages = []kuikv1alpha1.MatchingImage{
				{
					Image: "docker.io/library/nginx:latest",
					Mirrors: []kuikv1alpha1.MirrorStatus{
						{Image: "mirror.example.com/cache/library/nginx:latest"},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if errors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())
			resource.Finalizers = nil
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should persist unusedSince after reconciliation", func() {
			By("Reconciling with no pods using the image")
			beforeReconcile := time.Now().Truncate(time.Second)
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying unusedSince is set")
			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.MatchingImages).To(HaveLen(1))
			Expect(resource.Status.MatchingImages[0].UnusedSince).NotTo(BeNil())
			Expect(resource.Status.MatchingImages[0].UnusedSince.Time).To(BeTemporally(">=", beforeReconcile))
		})

		It("should not overwrite unusedSince on subsequent reconciliations", func() {
			By("Reconciling a first time to set unusedSince")
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.MatchingImages[0].UnusedSince).NotTo(BeNil())
			firstUnusedSince := resource.Status.MatchingImages[0].UnusedSince.Time

			By("Reconciling again")
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying unusedSince has not changed")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.MatchingImages).To(HaveLen(1))
			Expect(resource.Status.MatchingImages[0].UnusedSince).NotTo(BeNil())
			Expect(resource.Status.MatchingImages[0].UnusedSince.Time).To(BeTemporally("==", firstUnusedSince))
		})
	})

	// Regression coverage for commit d26a099: the reconciler must match its
	// ImageFilter against the pod's current (rewritten) image, not the
	// original image stashed in the kuik.enix.io/original-images annotation.
	Context("When a pod's image has been rewritten by the webhook", func() {
		const (
			resourceName   = "test-rewritten-cluster"
			podName        = "rewritten-pod-cluster"
			namespace      = "default"
			originalImage  = "docker.io/library/nginx:1.25"
			rewrittenImage = "rewritten.example.com/library/nginx:1.25"
			mirrorRegistry = "mirror.example.com"
			mirrorPath     = "cache"
			expectedMirror = "mirror.example.com/cache/library/nginx:1.25"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName}
		podNamespacedName := types.NamespacedName{Name: podName, Namespace: namespace}

		newClusterReconciler := func() *ClusterImageSetMirrorReconciler {
			return &ClusterImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				},
			}
		}

		createRewrittenPod := func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
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
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				p := &corev1.Pod{}
				if err := k8sClient.Get(ctx, podNamespacedName, p); err == nil {
					_ = k8sClient.Delete(ctx, p)
				}
			})
		}

		createCISM := func(filterInclude []string) {
			resource := &kuikv1alpha1.ClusterImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{
					Name:       resourceName,
					Finalizers: []string{imageSetMirrorFinalizer},
				},
				Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
					ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: filterInclude},
					Mirrors:     kuikv1alpha1.Mirrors{{Registry: mirrorRegistry, Path: mirrorPath}},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ClusterImageSetMirror{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					res.Finalizers = nil
					_ = k8sClient.Update(ctx, res)
					_ = k8sClient.Delete(ctx, res)
				}
			})
		}

		It("matches the pod's rewritten image when the filter includes the rewritten registry", func() {
			createRewrittenPod()
			createCISM([]string{`rewritten\.example\.com/.*`})

			By("Pre-seeding status so the mirror loop skips actual image copies")
			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			mirroredAt := metav1.NewTime(time.Now())
			resource.Status.MatchingImages = []kuikv1alpha1.MatchingImage{{
				Image: rewrittenImage,
				Mirrors: []kuikv1alpha1.MirrorStatus{
					{Image: expectedMirror, MirroredAt: &mirroredAt},
				},
			}}
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())

			By("Reconciling")
			_, err := newClusterReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the rewritten image stays in matchingImages and is not marked unused")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.MatchingImages).To(HaveLen(1))
			Expect(resource.Status.MatchingImages[0].Image).To(Equal(rewrittenImage))
			Expect(resource.Status.MatchingImages[0].UnusedSince).To(BeNil())
		})

		It("does not match the pod when only the original (pre-rewrite) image matches the filter", func() {
			createRewrittenPod()
			createCISM([]string{`docker\.io/library/nginx:.*`})

			By("Reconciling with an empty status (nothing seeded)")
			_, err := newClusterReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the original image was not picked up as a matching image")
			resource := &kuikv1alpha1.ClusterImageSetMirror{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			for _, mi := range resource.Status.MatchingImages {
				Expect(mi.Image).NotTo(Equal(originalImage))
			}
			Expect(resource.Status.MatchingImages).To(BeEmpty())
		})
	})

	// Regression coverage for https://github.com/enix/kube-image-keeper/issues/567.
	// Scenario: the upstream A registry is unreachable, so the webhook has
	// rewritten pod-2 to pull from mirror B. pod-1 (which pinned the original
	// A reference) is gone. The CISM status was populated earlier with the
	// original A image and its B mirror; that entry must not be marked unused
	// while pod-2 still depends on the mirror copy.
	Context("Issue #567: CISM must keep the original image in use when pods reference only the mirror", func() {
		const (
			resourceName   = "test-issue-567-cism"
			podName        = "issue-567-rewritten-pod"
			namespace      = "default"
			originalImage  = "a.example.com/test/foo:v1"
			mirrorRegistry = "b.example.com"
			rewrittenImage = "b.example.com/test/foo:v1"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName}
		podNamespacedName := types.NamespacedName{Name: podName, Namespace: namespace}

		newClusterReconciler := func() *ClusterImageSetMirrorReconciler {
			return &ClusterImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				},
			}
		}

		It("keeps unusedSince nil on the original image while a rewritten pod uses the mirror copy", func() {
			By("Creating pod-2 rewritten by the webhook (container image = mirror URL, annotation = original URL)")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
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
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				p := &corev1.Pod{}
				if err := k8sClient.Get(ctx, podNamespacedName, p); err == nil {
					_ = k8sClient.Delete(ctx, p)
				}
			})

			By("Creating a CISM whose filter targets the ORIGINAL registry A and whose mirror is B")
			cism := &kuikv1alpha1.ClusterImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{
					Name:       resourceName,
					Finalizers: []string{imageSetMirrorFinalizer},
				},
				Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
					ImageFilter: kuikv1alpha1.ImageFilterDefinition{
						Include: []string{`a\.example\.com/.*`},
					},
					Mirrors: kuikv1alpha1.Mirrors{
						{Registry: mirrorRegistry},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cism)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ClusterImageSetMirror{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					res.Finalizers = nil
					_ = k8sClient.Update(ctx, res)
					_ = k8sClient.Delete(ctx, res)
				}
			})

			By("Pre-seeding status as it would be after pod-1 mirrored the image — original image + already-mirrored B copy")
			Expect(k8sClient.Get(ctx, typeNamespacedName, cism)).To(Succeed())
			mirroredAt := metav1.NewTime(time.Now())
			cism.Status.MatchingImages = []kuikv1alpha1.MatchingImage{
				{
					Image: originalImage,
					Mirrors: []kuikv1alpha1.MirrorStatus{
						{Image: rewrittenImage, MirroredAt: &mirroredAt},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, cism)).To(Succeed())

			By("Reconciling after pod-1 has been deleted and only the rewritten pod-2 remains")
			_, err := newClusterReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the original image is still tracked and has not been marked unused")
			got := &kuikv1alpha1.ClusterImageSetMirror{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			var matching *kuikv1alpha1.MatchingImage
			for i := range got.Status.MatchingImages {
				if got.Status.MatchingImages[i].Image == originalImage {
					matching = &got.Status.MatchingImages[i]
					break
				}
			}
			Expect(matching).NotTo(BeNil(), "original image must remain in status.matchingImages")
			Expect(matching.UnusedSince).To(BeNil(),
				"unusedSince must stay nil while pod-2 still pulls from the mirror (issue #567)")
		})
	})
})
