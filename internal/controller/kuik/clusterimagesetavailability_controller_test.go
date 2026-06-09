package kuik

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

		It("should successfully reconcile the resource with a namespace filter", func() {
			By("Reconciling the created resource (empty namespace filter = all namespaces)")
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

	Context("When the CISA has namespace filters", func() {
		const (
			resourceName = "test-namespace-filter-cisa"
			image        = "docker.io/library/nginx:latest"
			nsInScope    = "cisa-ns-in-scope"
			nsOutScope   = "cisa-ns-out-scope"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName}

		ensureNamespace := func(name string) {
			err := k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			})
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		}

		createPod := func(name, namespace string) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: image},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pod)
			})
		}

		createCISAAndSeed := func(include, exclude []string) {
			// imageFilter and namespaceFilter both fold into the unified filter.
			filterInclude := []kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Image: `docker\.io/library/nginx:.*`}}}
			for _, ns := range include {
				filterInclude = append(filterInclude, kuikv1alpha1.ClusterFilterItem{Namespace: ns})
			}
			var filterExclude []kuikv1alpha1.ClusterFilterItem
			for _, ns := range exclude {
				filterExclude = append(filterExclude, kuikv1alpha1.ClusterFilterItem{Namespace: ns})
			}
			resource := &kuikv1alpha1.ClusterImageSetAvailability{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: kuikv1alpha1.ClusterImageSetAvailabilitySpec{
					Filter: kuikv1alpha1.ClusterFilter{Include: filterInclude, Exclude: filterExclude},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ClusterImageSetAvailability{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					_ = k8sClient.Delete(ctx, res)
				}
			})

			By("Pre-seeding status with a monitored image so performCheck is bypassed")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			lastMonitor := metav1.NewTime(time.Now())
			resource.Status.Images = []kuikv1alpha1.MonitoredImage{{
				Image:       image,
				Status:      kuikv1alpha1.ImageAvailabilityAvailable,
				LastMonitor: &lastMonitor,
			}}
			resource.Status.ImageCount = 1
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())
		}

		BeforeEach(func() {
			ensureNamespace(nsInScope)
			ensureNamespace(nsOutScope)
		})

		It("treats out-of-scope pods as not using the image when IncludeNamespaces is set", func() {
			createCISAAndSeed([]string{nsInScope}, nil)
			createPod("cisa-pod-out", nsOutScope)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).NotTo(BeNil(),
				"out-of-scope pod must not keep the image in-use")
		})

		It("keeps the image in-use when an in-scope pod references it", func() {
			createCISAAndSeed([]string{nsInScope}, nil)
			createPod("cisa-pod-in", nsInScope)
			createPod("cisa-pod-out", nsOutScope)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).To(BeNil(),
				"in-scope pod must keep the image in-use")
		})

		It("treats pods in excluded namespaces as not using the image", func() {
			createCISAAndSeed(nil, []string{nsOutScope})
			createPod("cisa-pod-out", nsOutScope)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).NotTo(BeNil(),
				"excluded-namespace pod must not keep the image in-use")
		})
	})

	Context("When the CISA has pod filters", func() {
		const (
			resourceName = "test-pod-filter-cisa"
			image        = "docker.io/library/nginx:latest"
			namespace    = "cisa-ns-pod-filter"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName}

		ensureNamespace := func(name string) {
			err := k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			})
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		}

		createPod := func(name string, labels, annotations map[string]string) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        name,
					Namespace:   namespace,
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: image},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pod)
			})
		}

		// createCISAAndSeed folds the nginx image dimension into spec.filter
		// alongside the given pod include/exclude items.
		createCISAAndSeed := func(include, exclude []kuikv1alpha1.ClusterFilterItem) {
			filterInclude := append([]kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Image: `docker\.io/library/nginx:.*`}}}, include...)
			resource := &kuikv1alpha1.ClusterImageSetAvailability{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: kuikv1alpha1.ClusterImageSetAvailabilitySpec{
					Filter: kuikv1alpha1.ClusterFilter{Include: filterInclude, Exclude: exclude},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				res := &kuikv1alpha1.ClusterImageSetAvailability{}
				if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
					_ = k8sClient.Delete(ctx, res)
				}
			})

			By("Pre-seeding status with a monitored image so performCheck is bypassed")
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			lastMonitor := metav1.NewTime(time.Now())
			resource.Status.Images = []kuikv1alpha1.MonitoredImage{{
				Image:       image,
				Status:      kuikv1alpha1.ImageAvailabilityAvailable,
				LastMonitor: &lastMonitor,
			}}
			resource.Status.ImageCount = 1
			Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())
		}

		BeforeEach(func() {
			ensureNamespace(namespace)
		})

		It("drops pods whose labels match an exclude selector", func() {
			createCISAAndSeed(nil, []kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Label: "cnpg.io/podRole=instance"}}})
			createPod("cisa-pod-excluded", map[string]string{"cnpg.io/podRole": "instance"}, nil)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).NotTo(BeNil(),
				"excluded-label pod must not keep the image in-use")
		})

		It("keeps pods whose labels don't match an exclude selector", func() {
			createCISAAndSeed(nil, []kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Label: "cnpg.io/podRole=instance"}}})
			createPod("cisa-pod-kept", map[string]string{"app": "foo"}, nil)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).To(BeNil(),
				"non-matching pod must keep the image in-use")
		})

		It("narrows to pods that match an include label selector", func() {
			createCISAAndSeed([]kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Label: "app=monitor-me"}}}, nil)
			createPod("cisa-pod-out", map[string]string{"app": "skip-me"}, nil)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).NotTo(BeNil(),
				"non-included pod must not keep the image in-use")
		})

		It("supports annotation presence-only includes", func() {
			createCISAAndSeed([]kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Annotation: "my.company.com/custom-annotation"}}}, nil)
			createPod("cisa-pod-no-anno", map[string]string{"app": "foo"}, nil)

			_, err := newTestReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &kuikv1alpha1.ClusterImageSetAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Status.Images).To(HaveLen(1))
			Expect(resource.Status.Images[0].UnusedSince).NotTo(BeNil(),
				"pod missing the required annotation must not keep the image in-use")
		})
	})

	Context("When the CISA has an invalid filter spec", func() {
		const resourceName = "test-invalid-filter-cisa"

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName}

		AfterEach(func() {
			res := &kuikv1alpha1.ClusterImageSetAvailability{}
			if err := k8sClient.Get(ctx, typeNamespacedName, res); err == nil {
				_ = k8sClient.Delete(ctx, res)
			}
		})

		It("does not retry on invalid filter and emits an InvalidFilter event", func() {
			resource := &kuikv1alpha1.ClusterImageSetAvailability{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
				Spec: kuikv1alpha1.ClusterImageSetAvailabilitySpec{
					Filter: kuikv1alpha1.ClusterFilter{
						Include: []kuikv1alpha1.ClusterFilterItem{{FilterItem: kuikv1alpha1.FilterItem{Label: "==="}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			r := newTestReconciler()
			rec := events.NewFakeRecorder(1)
			r.Recorder = rec

			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred(),
				"invalid filter spec must not bubble up as a reconcile error (would cause hot-loop)")
			Expect(result.RequeueAfter).To(BeZero())

			Eventually(rec.Events).Should(Receive(ContainSubstring("InvalidFilter")))
		})
	})
})
