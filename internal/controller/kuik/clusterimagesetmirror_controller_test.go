package kuik

import (
	"context"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
})
