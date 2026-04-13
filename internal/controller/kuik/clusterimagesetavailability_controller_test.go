package kuik

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
)

func newTestReconciler() *ClusterImageSetAvailabilityReconciler {
	cfg, err := config.LoadDefault()
	Expect(err).NotTo(HaveOccurred())
	return &ClusterImageSetAvailabilityReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
		Config: cfg,
	}
}

var _ = Describe("ClusterImageSetAvailability Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		clusterimagesetavailability := &kuikv1alpha1.ClusterImageSetAvailability{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ClusterImageSetAvailability")
			err := k8sClient.Get(ctx, typeNamespacedName, clusterimagesetavailability)
			if err != nil && errors.IsNotFound(err) {
				resource := &kuikv1alpha1.ClusterImageSetAvailability{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterImageSetAvailability")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := newTestReconciler()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should remove images unused for longer than the retention period", func() {
			By("Setting up a CISA with an expired unused image and a still-valid unused image")
			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			expiry := time.Hour
			expiredSince := metav1.NewTime(time.Now().Add(-2 * expiry))
			recentSince := metav1.NewTime(time.Now())

			resource.Spec.UnusedImageExpiry = metav1.Duration{Duration: expiry}
			resource.Spec.ImageFilter = kuikv1alpha1.ImageFilterDefinition{
				Include: []string{".*"},
			}
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			resource.Status.Images = []kuikv1alpha1.MonitoredImage{
				{Image: "docker.io/library/nginx:1.26", Status: kuikv1alpha1.ImageAvailabilityAvailable, UnusedSince: &expiredSince},
				{Image: "docker.io/library/redis:7", Status: kuikv1alpha1.ImageAvailabilityAvailable, UnusedSince: &recentSince},
			}
			resource.Status.ImageCount = 2
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())

			By("Verifying both images are present before reconciliation")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(2))

			By("Reconciling the resource")
			controllerReconciler := newTestReconciler()
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying only the expired image was removed")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].Image).To(Equal("docker.io/library/redis:7"))
		})
	})
})
