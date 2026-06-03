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

// Namespace filtering is unique to the cluster-scoped kind (ImageSetMirror has
// no namespaceFilter), so these specs stay ClusterImageSetMirror-only. The rest
// of the reconcile behaviour is exercised against both kinds in
// mirror_shared_test.go.
var _ = Describe("ClusterImageSetMirror namespace filtering", func() {
	const (
		resourceName = "test-namespace-filter"
		image        = "docker.io/library/nginx:latest"
		mirrorImage  = "mirror.example.com/cache/library/nginx:latest"
		nsInScope    = "ns-in-scope"
		nsOutScope   = "ns-out-scope"
	)

	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{Name: resourceName}

	newReconciler := func() *ClusterImageSetMirrorReconciler {
		return &ClusterImageSetMirrorReconciler{
			ImageSetMirrorBaseReconciler: ImageSetMirrorBaseReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			},
		}
	}

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

	createCISMAndSeed := func(include, exclude []string) {
		resource := &kuikv1alpha1.ClusterImageSetMirror{
			ObjectMeta: metav1.ObjectMeta{
				Name:       resourceName,
				Finalizers: []string{imageSetMirrorFinalizer},
			},
			Spec: kuikv1alpha1.ClusterImageSetMirrorSpec{
				ImageSetMirrorSpec: kuikv1alpha1.ImageSetMirrorSpec{
					ImageFilter: kuikv1alpha1.ImageFilterDefinition{
						Include: []string{`docker\.io/library/nginx:.*`},
					},
					Mirrors: kuikv1alpha1.Mirrors{
						{Registry: "mirror.example.com", Path: "cache"},
					},
				},
				NamespaceFilter: kuikv1alpha1.NamespaceFilterDefinition{
					Include: include,
					Exclude: exclude,
				},
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

		By("Pre-seeding status so the reconciler skips the actual mirror push")
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		mirroredAt := metav1.NewTime(time.Now())
		resource.Status.MatchingImages = []kuikv1alpha1.MatchingImage{{
			Image: image,
			Mirrors: []kuikv1alpha1.MirrorStatus{
				{Image: mirrorImage, MirroredAt: &mirroredAt},
			},
		}}
		Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())
	}

	BeforeEach(func() {
		ensureNamespace(nsInScope)
		ensureNamespace(nsOutScope)
	})

	It("treats out-of-scope pods as not using the image when IncludeNamespaces is set", func() {
		createCISMAndSeed([]string{nsInScope}, nil)
		createPod("pod-out", nsOutScope)

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		resource := &kuikv1alpha1.ClusterImageSetMirror{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		Expect(resource.Status.MatchingImages).To(HaveLen(1))
		Expect(resource.Status.MatchingImages[0].UnusedSince).NotTo(BeNil(),
			"out-of-scope pod must not keep the image in-use")
	})

	It("keeps the image in-use when an in-scope pod references it", func() {
		createCISMAndSeed([]string{nsInScope}, nil)
		createPod("pod-in", nsInScope)
		createPod("pod-out", nsOutScope)

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		resource := &kuikv1alpha1.ClusterImageSetMirror{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		Expect(resource.Status.MatchingImages).To(HaveLen(1))
		Expect(resource.Status.MatchingImages[0].UnusedSince).To(BeNil(),
			"in-scope pod must keep the image in-use")
	})

	It("treats pods in excluded namespaces as not using the image", func() {
		createCISMAndSeed(nil, []string{nsOutScope})
		createPod("pod-out", nsOutScope)

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		resource := &kuikv1alpha1.ClusterImageSetMirror{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
		Expect(resource.Status.MatchingImages).To(HaveLen(1))
		Expect(resource.Status.MatchingImages[0].UnusedSince).NotTo(BeNil(),
			"excluded-namespace pod must not keep the image in-use")
	})
})
