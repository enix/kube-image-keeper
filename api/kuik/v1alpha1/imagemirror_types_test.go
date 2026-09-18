package v1alpha1

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newImageMirror(path string) *ImageMirror {
	return &ImageMirror{
		ObjectMeta: metav1.ObjectMeta{GenerateName: testGenerateName},
		Spec:       ImageMirrorSpec{Destination: MirrorDestination{Path: path}},
	}
}

var _ = Describe("ImageMirror", func() {
	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(ctx, &ImageMirror{})).To(Succeed())
	})

	Context("defaults", func() {
		It("defaults rewritePolicy to OnFailure, driftPolicy to Ignore and cleanup to enabled with a 168h retention", func() {
			im := newImageMirror("registry.tld/mirror/")
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
			Expect(im.Spec.RewritePolicy).To(Equal(RewritePolicyOnFailure))
			Expect(im.Spec.DriftPolicy).To(Equal(DriftPolicyIgnore))
			Expect(im.Spec.Cleanup).NotTo(BeNil())
			Expect(im.Spec.Cleanup.Enabled).To(HaveValue(BeTrue()))
			Expect(im.Spec.Cleanup.Retention).To(HaveValue(Equal(metav1.Duration{Duration: 168 * time.Hour})))
		})

		It("keeps an explicit cleanup.enabled false and the default retention", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.Cleanup = &Cleanup{Enabled: new(false)}
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
			Expect(im.Spec.Cleanup.Enabled).To(HaveValue(BeFalse()))
			Expect(im.Spec.Cleanup.Retention).To(HaveValue(Equal(metav1.Duration{Duration: 168 * time.Hour})))
		})
	})

	Context("rewritePolicy and driftPolicy", func() {
		It("accepts None as rewritePolicy, to copy without ever routing", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.RewritePolicy = RewritePolicyNone
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
		})

		It("rejects a rewritePolicy outside OnFailure, Always and None", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.RewritePolicy = "Sometimes"
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, im))).To(BeTrue())
		})

		It("accepts Warn and Sync as driftPolicy", func() {
			for _, p := range []DriftPolicy{DriftPolicyWarn, DriftPolicySync} {
				im := newImageMirror("registry.tld/mirror/")
				im.Spec.DriftPolicy = p
				Expect(k8sClient.Create(ctx, im)).To(Succeed(), "%s", p)
			}
		})

		It("rejects a driftPolicy outside Ignore, Warn and Sync", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.DriftPolicy = "Resync"
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, im))).To(BeTrue())
		})
	})

	Context("destination", func() {
		It("accepts a path with a trailing slash", func() {
			Expect(k8sClient.Create(ctx, newImageMirror("registry.tld/mirror/"))).To(Succeed())
		})

		It("accepts a path without a trailing slash", func() {
			Expect(k8sClient.Create(ctx, newImageMirror("registry.tld/mirror"))).To(Succeed())
		})

		It("accepts a registry host alone as the path", func() {
			Expect(k8sClient.Create(ctx, newImageMirror("registry.local:5000"))).To(Succeed())
		})

		It("rejects a path carrying a tag", func() {
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, newImageMirror("registry.tld/mirror:latest")))).To(BeTrue())
		})

		It("rejects a path that is not fully qualified with a registry host", func() {
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, newImageMirror("mirror/")))).To(BeTrue())
		})

		It("rejects a missing destination path", func() {
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, newImageMirror("")))).To(BeTrue())
		})

		It("accepts distinct manage and pull credentials", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.Destination.Manage = &DestinationCredentials{Auth: Auth{SecretRef: &SecretReference{Name: "mirror-write"}}}
			im.Spec.Destination.Pull = &DestinationCredentials{Auth: Auth{SecretRef: &SecretReference{Name: "mirror-read"}}}
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
		})

		It("rejects a manage auth carrying both secretRef and provider", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.Destination.Manage = &DestinationCredentials{Auth: Auth{
				SecretRef: &SecretReference{Name: "mirror-write"},
				Provider:  &Provider{Name: ProviderAzure},
			}}
			err := k8sClient.Create(ctx, im)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exactly one of secretRef or provider")))
		})
	})

	Context("excludeImages", func() {
		It("accepts repository globs and tag globs", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.ExcludeImages = []string{"ghcr.io/foo-bar/**", "quay.io/acme/*-debug", "**:latest", "docker.io/library/nginx"}
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
		})
	})

	Context("status", func() {
		It("stores the copy side, the routing side and the conditions", func() {
			im := newImageMirror("registry.tld/mirror/")
			im.Spec.DriftPolicy = DriftPolicyWarn
			Expect(k8sClient.Create(ctx, im)).To(Succeed())

			now := metav1.Now()
			im.Status = ImageMirrorStatus{
				Images: &MirrorImages{Copy: &CopyCounts{Tracked: 312, Running: 305, Standby: 5, Retained: 2, Available: 309, Unavailable: 3, MissingSource: 1, OrphanTags: 3}},
				DriftedImages: []MirrorDriftedImage{{
					Ref: "docker.io/acme/app:prod", UpstreamDigest: "sha256:bbbb", CopiedDigest: digestA, Since: now,
				}},
				FailedImageCopies: []FailedImageCopy{{Ref: "quay.io/acme/tool:1.4", Reason: CopySourceNotFound, Since: now, LastAttempt: &now}},
				PendingDeletion: []PendingDeletion{
					{Ref: "registry.tld/mirror/ghcr.io/acme/report-job:v42_cluster-a", Origin: "ghcr.io/acme/report-job:v42", UnusedSince: now},
					{Ref: "registry.tld/mirror/quay.io/acme/tool:1.3_cluster-a", UnusedSince: now},
				},
				SelfChecked:  &now,
				Repositories: []string{"registry.tld/mirror/ghcr.io/acme/report-job", "registry.tld/mirror/quay.io/acme/tool"},
				Checks: &ChecksStatus{Registries: []RegistryCheck{{
					Registry: "quay.io", Images: 41, Cursor: thanosImage, CycleStarted: &now,
					CycleDuration: &metav1.Duration{Duration: 2 * time.Hour},
				}}},
				RoutingStatus: RoutingStatus{
					Pods:       &PodCounts{Tracked: 480, Rewritten: 455},
					Containers: &ContainerCounts{Tracked: 1104, Untouched: 620, Rewritten: 481, Conceded: 1, Stale: 1, NoAlternatives: 1},
				},
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue, Reason: ReasonIsReady, LastTransitionTime: now},
					{Type: ConditionDestinationOutOfSync, Status: metav1.ConditionTrue, Reason: ReasonMissingImages, Message: "3 images not copied yet", LastTransitionTime: now},
				},
			}
			Expect(k8sClient.Status().Update(ctx, im)).To(Succeed())

			got := &ImageMirror{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(im), got)).To(Succeed())
			Expect(got.Status.Images.Copy.Tracked).To(BeEquivalentTo(312))
			Expect(got.Status.PendingDeletion).To(HaveLen(2))
			Expect(got.Status.Repositories).To(HaveLen(2))
			Expect(got.Status.Checks.Registries[0].CycleDuration.Duration).To(Equal(2 * time.Hour))
			Expect(got.Status.Pods.Rewritten).To(BeEquivalentTo(455))
		})

		It("rejects a copy failure reason outside the shared vocabulary", func() {
			im := newImageMirror("registry.tld/mirror/")
			Expect(k8sClient.Create(ctx, im)).To(Succeed())
			im.Status.FailedImageCopies = []FailedImageCopy{{Ref: "quay.io/acme/tool:1.4", Reason: "Timeout", Since: metav1.Now()}}
			Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, im))).To(BeTrue())
		})
	})
})

var _ = Describe("Cleanup", func() {
	Describe("IsEnabled", func() {
		It("is enabled when cleanup is absent", func() {
			var c *Cleanup
			Expect(c.IsEnabled()).To(BeTrue())
		})

		It("is enabled when enabled is unset", func() {
			Expect((&Cleanup{}).IsEnabled()).To(BeTrue())
		})

		It("is disabled when enabled is false", func() {
			Expect((&Cleanup{Enabled: new(false)}).IsEnabled()).To(BeFalse())
		})
	})
})
