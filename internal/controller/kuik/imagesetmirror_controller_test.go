package kuik

import (
	"context"
	"fmt"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// conflictOnFirstUpdateClient wraps a client.Client and returns a conflict
// error on the first Update call, then delegates to the real client.
type conflictOnFirstUpdateClient struct {
	client.Client
	conflicted bool
}

func (c *conflictOnFirstUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if !c.conflicted {
		c.conflicted = true
		return errors.NewConflict(
			schema.GroupResource{Group: "kuik.enix.io", Resource: "imagesetmirrors"},
			obj.GetName(),
			fmt.Errorf("simulated conflict"),
		)
	}
	return c.Client.Update(ctx, obj, opts...)
}

var _ = Describe("ImageSetMirror Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		imagesetmirror := &kuikv1alpha1.ImageSetMirror{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ImageSetMirror")
			err := k8sClient.Get(ctx, typeNamespacedName, imagesetmirror)
			if err != nil && errors.IsNotFound(err) {
				resource := &kuikv1alpha1.ImageSetMirror{
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
			resource := &kuikv1alpha1.ImageSetMirror{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ImageSetMirror")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})

	Context("When a conflict occurs during finalizer operations", func() {
		const resourceName = "test-conflict"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		newReconciler := func(c client.Client) *ImageSetMirrorReconciler {
			return &ImageSetMirrorReconciler{
				ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
					Client: c,
					Scheme: k8sClient.Scheme(),
				},
			}
		}

		It("should retry and succeed when a conflict occurs while adding the finalizer", func() {
			By("Creating an ImageSetMirror without a finalizer")
			resource := &kuikv1alpha1.ImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ImageSetMirror{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					res.Finalizers = nil
					_ = k8sClient.Update(ctx, res)
					_ = k8sClient.Delete(ctx, res)
				}
			})

			By("Reconciling with a client that returns a conflict on the first Update")
			wrappedClient := &conflictOnFirstUpdateClient{Client: k8sClient}
			_, err := newReconciler(wrappedClient).Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the finalizer was added")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(resource, imageSetMirrorFinalizer)).To(BeTrue())
		})

		It("should retry and succeed when a conflict occurs while removing the finalizer", func() {
			By("Creating an ImageSetMirror with a finalizer")
			resource := &kuikv1alpha1.ImageSetMirror{
				ObjectMeta: metav1.ObjectMeta{
					Name:       resourceName,
					Namespace:  "default",
					Finalizers: []string{imageSetMirrorFinalizer},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ImageSetMirror{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					res.Finalizers = nil
					_ = k8sClient.Update(ctx, res)
					_ = k8sClient.Delete(ctx, res)
				}
			})

			By("Deleting the resource to set DeletionTimestamp")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Reconciling with a client that returns a conflict on the first Update")
			wrappedClient := &conflictOnFirstUpdateClient{Client: k8sClient}
			_, err := newReconciler(wrappedClient).Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the resource was fully deleted")
			err = k8sClient.Get(ctx, typeNamespacedName, &kuikv1alpha1.ImageSetMirror{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
