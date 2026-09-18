package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newImageAlternative(alternatives ...Alternative) *ImageAlternative {
	return &ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{GenerateName: testGenerateName},
		Spec:       ImageAlternativeSpec{Alternatives: alternatives},
	}
}

var _ = Describe("ImageAlternative", func() {
	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(ctx, &ImageAlternative{})).To(Succeed())
	})

	Context("alternatives entries", func() {
		It("accepts an entry naming a single repository", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("accepts an entry naming a repository group", func() {
			ia := newImageAlternative(Alternative{RepositoryGroup: "quay.io/acme"})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("accepts a repository whose registry host carries a port", func() {
			ia := newImageAlternative(Alternative{Repository: "registry.local:5000/mirror/acme/foo"})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("accepts a repository group naming a whole registry host", func() {
			ia := newImageAlternative(Alternative{RepositoryGroup: dockerHub})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("rejects an entry carrying both repository and repositoryGroup", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo, RepositoryGroup: "quay.io/acme"})
			err := k8sClient.Create(ctx, ia)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exactly one of repository or repositoryGroup")))
		})

		It("rejects an entry carrying neither repository nor repositoryGroup", func() {
			ia := newImageAlternative(Alternative{Insecure: true})
			err := k8sClient.Create(ctx, ia)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exactly one of repository or repositoryGroup")))
		})

		It("rejects a repository carrying a tag", func() {
			ia := newImageAlternative(Alternative{Repository: "quay.io/acme/foo:v1"})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a repository carrying a digest", func() {
			ia := newImageAlternative(Alternative{Repository: "quay.io/acme/foo@sha256:0123456789abcdef"})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a repository group carrying a tag", func() {
			ia := newImageAlternative(Alternative{RepositoryGroup: "quay.io/acme:v1"})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a repository naming a whole registry host", func() {
			ia := newImageAlternative(Alternative{Repository: dockerHub})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a repository that is not fully qualified with a registry host", func() {
			ia := newImageAlternative(Alternative{Repository: "acme/foo"})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a repository with an uppercase path component", func() {
			ia := newImageAlternative(Alternative{Repository: "quay.io/Acme/foo"})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a list mixing repository and repositoryGroup entries", func() {
			ia := newImageAlternative(
				Alternative{Repository: acmeFoo},
				Alternative{RepositoryGroup: "docker.io/acme-org/foo"},
			)
			err := k8sClient.Create(ctx, ia)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("all be repository entries or all be repositoryGroup entries")))
		})

		It("rejects an empty alternatives list", func() {
			ia := newImageAlternative()
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("keeps the declared order of the entries", func() {
			ia := newImageAlternative(
				Alternative{Repository: acmeFoo},
				Alternative{Repository: "docker.io/acme-org/foo"},
				Alternative{Repository: "registry.local:5000/mirror/acme/foo", Unavailable: true},
			)
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
			Expect(ia.Spec.Alternatives).To(HaveExactElements(
				HaveField("Repository", acmeFoo),
				HaveField("Repository", "docker.io/acme-org/foo"),
				HaveField("Unavailable", true),
			))
		})
	})

	Context("rewritePolicy", func() {
		It("defaults rewritePolicy to OnFailure", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
			Expect(ia.Spec.RewritePolicy).To(Equal(RewritePolicyOnFailure))
		})

		It("accepts Always", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo})
			ia.Spec.RewritePolicy = RewritePolicyAlways
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("rejects None, which only an ImageMirror accepts", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo})
			ia.Spec.RewritePolicy = RewritePolicyNone
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})
	})

	Context("auth", func() {
		It("accepts a secretRef", func() {
			ia := newImageAlternative(Alternative{
				Repository: acmeFoo,
				Auth:       &Auth{SecretRef: &SecretReference{Name: quayPullSecret}},
			})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("accepts a provider with a serviceAccountRef", func() {
			ia := newImageAlternative(Alternative{
				Repository: "123456.dkr.ecr.eu-west-3.amazonaws.com/repo/acme/foo",
				Auth: &Auth{Provider: &Provider{
					Name:              ProviderAWS,
					ServiceAccountRef: &ServiceAccountReference{Name: "kuik-ecr-access"},
				}},
			})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())
		})

		It("rejects an auth carrying both secretRef and provider", func() {
			ia := newImageAlternative(Alternative{
				Repository: acmeFoo,
				Auth: &Auth{
					SecretRef: &SecretReference{Name: quayPullSecret},
					Provider:  &Provider{Name: ProviderGCP},
				},
			})
			err := k8sClient.Create(ctx, ia)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exactly one of secretRef or provider")))
		})

		It("rejects an auth carrying neither secretRef nor provider", func() {
			ia := newImageAlternative(Alternative{
				Repository: acmeFoo,
				Auth:       &Auth{InjectPullSecret: new(true)},
			})
			err := k8sClient.Create(ctx, ia)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exactly one of secretRef or provider")))
		})

		It("rejects a provider outside aws, gcp and azure", func() {
			ia := newImageAlternative(Alternative{
				Repository: acmeFoo,
				Auth:       &Auth{Provider: &Provider{Name: "oci"}},
			})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})

		It("rejects a secretRef with an empty name", func() {
			ia := newImageAlternative(Alternative{
				Repository: acmeFoo,
				Auth:       &Auth{SecretRef: &SecretReference{}},
			})
			Expect(apierrors.IsInvalid(k8sClient.Create(ctx, ia))).To(BeTrue())
		})
	})

	Context("status", func() {
		It("stores the routing status and its conditions", func() {
			ia := newImageAlternative(Alternative{Repository: acmeFoo})
			Expect(k8sClient.Create(ctx, ia)).To(Succeed())

			now := metav1.Now()
			ia.Status = ImageAlternativeStatus{
				RoutingStatus: RoutingStatus{
					Pods:       &PodCounts{Tracked: 123, Rewritten: 12},
					Containers: &ContainerCounts{Tracked: 281, Untouched: 265, Rewritten: 13, Conceded: 1, NoAlternatives: 2},
					ActiveFallbacks: []ActiveFallback{{
						Image: thanosImage, RewrittenTo: "ghcr.io/thanos-io/thanos:v0.42.2", Pods: 12, Since: now,
					}},
					NoAlternatives: []NoAlternative{{Image: "quay.io/thanos/thanos:v0.42.2-debug", Pods: 2, Since: now}},
					ConcededRewrites: []ReplacedRewrite{{
						Image: "quay.io/oauth2-proxy/oauth2-proxy:v7.7.1", RewrittenTo: "registry.tld/mirror/quay.io/oauth2-proxy/oauth2-proxy:v7.7.1_cluster-a",
						ReplacedBy: "internal.tld/oauth2-proxy:v7.7.1", Pods: 1, Since: now,
					}},
				},
				Truncated: map[string]int32{"noAlternatives": 12},
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue, Reason: ReasonIsReady, LastTransitionTime: now},
					{Type: ConditionFallbackActive, Status: metav1.ConditionTrue, Reason: ReasonOriginUnavailable, Message: "1 image routed to fallback (12 pods)", LastTransitionTime: now},
				},
			}
			Expect(k8sClient.Status().Update(ctx, ia)).To(Succeed())

			got := &ImageAlternative{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ia), got)).To(Succeed())
			Expect(got.Status.Pods.Tracked).To(BeEquivalentTo(123))
			Expect(got.Status.ActiveFallbacks).To(HaveLen(1))
			Expect(got.Status.Truncated).To(HaveKeyWithValue("noAlternatives", int32(12)))
			Expect(got.Status.Conditions).To(HaveLen(2))
		})
	})
})

var _ = Describe("Auth", func() {
	Describe("InjectsPullSecret", func() {
		It("injects the pull secret by default with a secretRef", func() {
			a := &Auth{SecretRef: &SecretReference{Name: quayPullSecret}}
			Expect(a.InjectsPullSecret()).To(BeTrue())
		})

		It("does not inject by default with a provider", func() {
			a := &Auth{Provider: &Provider{Name: ProviderAWS}}
			Expect(a.InjectsPullSecret()).To(BeFalse())
		})

		It("honours an explicit false with a secretRef", func() {
			a := &Auth{SecretRef: &SecretReference{Name: quayPullSecret}, InjectPullSecret: new(false)}
			Expect(a.InjectsPullSecret()).To(BeFalse())
		})

		It("honours an explicit true with a provider", func() {
			a := &Auth{Provider: &Provider{Name: ProviderGCP}, InjectPullSecret: new(true)}
			Expect(a.InjectsPullSecret()).To(BeTrue())
		})

		It("injects nothing when there is no auth at all", func() {
			var a *Auth
			Expect(a.InjectsPullSecret()).To(BeFalse())
		})
	})
})
