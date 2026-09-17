package v1alpha1

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newImageMonitor() *ImageMonitor {
	return &ImageMonitor{ObjectMeta: metav1.ObjectMeta{GenerateName: testGenerateName}}
}

var _ = Describe("ImageMonitor", func() {
	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(ctx, &ImageMonitor{})).To(Succeed())
	})

	Context("defaults", func() {
		It("defaults unusedImageRetention to 168h, driftDetection to true and monitorAlternatives to false", func() {
			m := newImageMonitor()
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
			Expect(m.Spec.UnusedImageRetention).To(HaveValue(Equal(metav1.Duration{Duration: 168 * time.Hour})))
			Expect(m.Spec.DriftDetection).To(HaveValue(BeTrue()))
			Expect(m.Spec.MonitorAlternatives).To(HaveValue(BeFalse()))
		})

		It("accepts an empty spec, which monitors every pod of the cluster", func() {
			m := &ImageMonitor{ObjectMeta: metav1.ObjectMeta{GenerateName: testGenerateName}, Spec: ImageMonitorSpec{}}
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
		})
	})

	Context("unusedImageRetention", func() {
		It("accepts a Go duration", func() {
			m := newImageMonitor()
			m.Spec.UnusedImageRetention = &metav1.Duration{Duration: 30 * time.Minute}
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
			Expect(m.Spec.UnusedImageRetention.Duration).To(Equal(30 * time.Minute))
		})

		It("rejects a retention that is not a duration", func() {
			m := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": SchemeGroupVersion.String(),
				"kind":       "ImageMonitor",
				"metadata":   map[string]any{"generateName": "test-"},
				"spec":       map[string]any{"unusedImageRetention": "7 days"},
			}}
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, m))).To(BeTrue())
		})
	})

	Context("status", func() {
		It("stores both image populations, the anomaly lists and the check rings", func() {
			m := newImageMonitor()
			m.Spec.MonitorAlternatives = new(true)
			Expect(k8sClient.Create(ctx, m)).To(Succeed())

			now := metav1.Now()
			m.Status = ImageMonitorStatus{
				Images: &MonitorImages{
					Origin: &OriginImageCounts{
						ImageCounts: ImageCounts{Tracked: 3241, Running: 3168, Standby: 12, Retained: 61, Available: 3226, Unavailable: 4},
						Drifted:     2,
					},
					Alternatives: &ImageCounts{Tracked: 214, Running: 9, Standby: 201, Retained: 4, Available: 211, Unavailable: 2},
				},
				RetainedImages:    []RetainedImage{{Ref: "ghcr.io/acme/report-job:v42", UnusedSince: now, Digest: digestA}},
				UnavailableImages: []UnavailableImage{{Ref: "docker.io/foo/bar:1.2", Reason: CheckManifestNotFound, Since: now, Pods: 3}},
				UnavailableAlternatives: []UnavailableAlternative{{
					Ref: "ghcr.io/thanos-io/thanos:v0.42.2", DerivedFrom: thanosImage, Via: "ImageAlternative/thanos",
					Reason: CheckUnauthorized, Since: now,
				}},
				DriftedImages: []MonitorDriftedImage{{
					Ref: "docker.io/acme/app:prod", UpstreamDigest: "sha256:bbbb", Since: now,
					RunningDigests: []RunningDigest{{Digest: digestA, Pods: 5}, {Digest: "sha256:cccc", Pods: 2}},
				}},
				Checks: &ChecksStatus{Registries: []RegistryCheck{
					{Registry: dockerHub, Images: 2140, Cursor: "docker.io/library/nginx:1.27", CycleStarted: &now, CycleDuration: &metav1.Duration{Duration: 35*time.Hour + 40*time.Minute}},
					{Registry: "quay.io", Images: 1101, Cursor: thanosImage, CycleStarted: &now},
				}},
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue, Reason: ReasonIsReady, LastTransitionTime: now},
					{Type: ConditionImagesUnavailable, Status: metav1.ConditionTrue, Reason: ReasonChecksFailed, LastTransitionTime: now},
					{Type: ConditionImagesDrifted, Status: metav1.ConditionTrue, Reason: ReasonUpstreamDigestMoved, LastTransitionTime: now},
				},
			}
			Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

			got := &ImageMonitor{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), got)).To(Succeed())
			Expect(got.Status.Images.Origin.Tracked).To(BeEquivalentTo(3241))
			Expect(got.Status.Images.Origin.Drifted).To(BeEquivalentTo(2))
			Expect(got.Status.DriftedImages[0].RunningDigests).To(HaveLen(2))
			Expect(got.Status.Checks.Registries).To(HaveLen(2))
			Expect(got.Status.Conditions).To(HaveLen(3))
		})

		It("has no drifted gauge on the alternatives population", func() {
			m := newImageMonitor()
			Expect(k8sClient.Create(ctx, m)).To(Succeed())

			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(SchemeGroupVersion.WithKind("ImageMonitor"))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), u)).To(Succeed())
			Expect(unstructured.SetNestedField(u.Object, map[string]any{
				"alternatives": map[string]any{
					"tracked": int64(1), "running": int64(0), "standby": int64(1), "retained": int64(0),
					"available": int64(1), "unavailable": int64(0), "drifted": int64(3),
				},
			}, "status", "images")).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, u)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), u)).To(Succeed())
			alternatives, _, err := unstructured.NestedMap(u.Object, "status", "images", "alternatives")
			Expect(err).NotTo(HaveOccurred())
			Expect(alternatives).To(HaveKeyWithValue("tracked", int64(1)))
			Expect(alternatives).NotTo(HaveKey("drifted"), "the schema prunes the field the spec does not define")
		})

		It("rejects two check rings for the same registry", func() {
			m := newImageMonitor()
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
			m.Status.Checks = &ChecksStatus{Registries: []RegistryCheck{
				{Registry: dockerHub, Images: 1},
				{Registry: dockerHub, Images: 2},
			}}
			Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, m))).To(BeTrue())
		})

		It("rejects a check failure reason outside the shared vocabulary", func() {
			m := newImageMonitor()
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
			m.Status.UnavailableImages = []UnavailableImage{{Ref: "docker.io/foo/bar:1.2", Reason: "SourceNotFound", Since: metav1.Now()}}
			Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, m))).To(BeTrue())
		})
	})
})

var _ = Describe("ImageMonitorSpec", func() {
	It("detects drift unless driftDetection is false", func() {
		Expect((&ImageMonitorSpec{}).DetectsDrift()).To(BeTrue())
		Expect((&ImageMonitorSpec{DriftDetection: new(false)}).DetectsDrift()).To(BeFalse())
	})

	It("monitors alternatives only when monitorAlternatives is true", func() {
		Expect((&ImageMonitorSpec{}).MonitorsAlternatives()).To(BeFalse())
		Expect((&ImageMonitorSpec{MonitorAlternatives: new(true)}).MonitorsAlternatives()).To(BeTrue())
	})
})
