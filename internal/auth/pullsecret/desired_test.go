package pullsecret

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/auth"
	"github.com/enix/kube-image-keeper/internal/routing/podrecord"
)

const (
	always    = kuikv1alpha1.RewritePolicyAlways
	onFailure = kuikv1alpha1.RewritePolicyOnFailure
)

var (
	teamA = namespace("team-a", map[string]string{teamLabel: "a"})
	teamB = namespace("team-b", map[string]string{teamLabel: "b"})
)

var _ = Describe("Needed namespaces", func() {
	Context("under rewritePolicy Always", func() {
		It("needs every namespace the namespaceSelector selects, with no pod in it", func() {
			cr := alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, nil))
			cr.Spec.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{teamLabel: "a"}}
			r := FromImageAlternative(cr)

			Expect(r.Needed(teamA, nil)).NotTo(BeEmpty())
			Expect(r.Needed(teamB, nil)).To(BeEmpty())
		})

		DescribeTable("selects every namespace of the cluster",
			func(selector *metav1.LabelSelector) {
				cr := alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, nil))
				cr.Spec.NamespaceSelector = selector
				r := FromImageAlternative(cr)

				Expect(r.Needed(teamA, nil)).NotTo(BeEmpty())
				Expect(r.Needed(namespace("unlabelled", nil), nil)).NotTo(BeEmpty())
			},
			Entry("when namespaceSelector is absent", nil),
			Entry("when namespaceSelector is empty", &metav1.LabelSelector{}),
		)

		It("does not narrow the namespaces by podSelector", func() {
			cr := alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, nil))
			cr.Spec.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "none"}}

			Expect(FromImageAlternative(cr).Needed(teamA, nil)).NotTo(BeEmpty())
		})

		It("needs no namespace when no entry of the resource injects", func() {
			cr := alternative(always, group(quayGroup, nil),
				group(ghcrGroup, &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: ghcrSecret}, InjectPullSecret: injectFalse}))

			Expect(FromImageAlternative(cr).Needed(teamA, nil)).To(BeEmpty())
		})
	})

	Context("under rewritePolicy OnFailure", func() {
		var r Resource

		BeforeEach(func() {
			r = FromImageAlternative(alternative(onFailure, group(quayGroup, nil), group(ghcrGroup, secretRef(ghcrSecret))))
		})

		It("needs a namespace once a live pod there carries a standing rewrite by the resource", func() {
			Expect(r.Needed(teamA, nil)).To(BeEmpty())

			pod := rewrittenPod("ImageAlternative/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")
			Expect(r.Needed(teamA, []*corev1.Pod{pod})).NotTo(BeEmpty())
		})

		It("ignores a rewrite whose container no longer runs the reference kuik placed", func() {
			pod := rewrittenPod("ImageAlternative/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:2")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})

		It("ignores a container whose rewrite by the resource was conceded", func() {
			pod := rewrittenPod("ImageAlternative/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")
			annotate(pod, podrecord.AnnotationConcededRewrites, "ImageAlternative/"+crName, "ghcr.io/acme/foo:1")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})

		It("ignores a rewrite by another resource of the same name and another kind", func() {
			pod := rewrittenPod("ImageMirror/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})

		It("ignores a rewrite whose by does not name a routing resource", func() {
			pod := rewrittenPod(crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})

		It("ignores a pod that has terminated", func() {
			pod := rewrittenPod("ImageAlternative/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")
			pod.Status.Phase = corev1.PodSucceeded

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})
	})

	It("needs no namespace for an ImageMirror under rewritePolicy None", func() {
		r := FromImageMirror(mirror(kuikv1alpha1.RewritePolicyNone, secretRef("mirror")))
		pod := rewrittenPod("ImageMirror/"+crName, "registry.tld/mirror/quay.io/acme/foo:1_c", "registry.tld/mirror/quay.io/acme/foo:1_c")

		Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
	})
})

var _ = Describe("Desired entries", func() {
	Context("under rewritePolicy Always", func() {
		It("holds every entry whose auth injects", func() {
			inject := &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: ghcrSecret}, InjectPullSecret: new(true)}
			r := FromImageAlternative(alternative(always,
				group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, inject), group("docker.io/acme", nil)))

			Expect(r.Needed(teamA, nil)).To(ConsistOf(
				Credential{Path: quayGroup, SecretRef: quaySecret},
				Credential{Path: ghcrGroup, SecretRef: ghcrSecret},
			))
		})

		DescribeTable("leaves out an entry",
			func(left kuikv1alpha1.Alternative) {
				r := FromImageAlternative(alternative(always, group(quayGroup, secretRef(quaySecret)), left))

				Expect(r.Needed(teamA, nil)).To(ConsistOf(Credential{Path: quayGroup, SecretRef: quaySecret}))
			},
			Entry("without auth", group(ghcrGroup, nil)),
			Entry("with a secretRef and injectPullSecret false", group(ghcrGroup,
				&kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: ghcrSecret}, InjectPullSecret: injectFalse})),
			Entry("with a provider and injectPullSecret unset", group(ghcrGroup,
				&kuikv1alpha1.Auth{Provider: &kuikv1alpha1.Provider{Name: "aws"}})),
			Entry("with a provider and injectPullSecret true, the provider being deferred", group(ghcrGroup,
				&kuikv1alpha1.Auth{Provider: &kuikv1alpha1.Provider{Name: "aws"}, InjectPullSecret: new(true)})),
			Entry("marked unavailable", kuikv1alpha1.Alternative{RepositoryGroup: ghcrGroup, Unavailable: true, Auth: secretRef(ghcrSecret)}),
		)

		It("holds the destination.pull credential of an ImageMirror", func() {
			r := FromImageMirror(mirror(always, secretRef("mirror")))

			Expect(r.Needed(teamA, nil)).To(ConsistOf(Credential{Path: "registry.tld/mirror", SecretRef: "mirror"}))
		})

		It("never holds the destination.manage credential of an ImageMirror, even with injectPullSecret true", func() {
			cr := mirror(always, nil)
			cr.Spec.Destination.Manage = &kuikv1alpha1.DestinationCredentials{Auth: kuikv1alpha1.Auth{
				SecretRef: &kuikv1alpha1.SecretReference{Name: "pusher"}, InjectPullSecret: new(true),
			}}

			Expect(FromImageMirror(cr).Needed(teamA, nil)).To(BeEmpty())
		})
	})

	Context("under rewritePolicy OnFailure", func() {
		DescribeTable("holds the entry that served the rewrite",
			func(r Resource, by, rewrittenTo string, want Credential) {
				pod := rewrittenPod(by, rewrittenTo, rewrittenTo)

				Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(ConsistOf(want))
			},
			Entry("a repository entry equal to the rewritten reference's repository",
				FromImageAlternative(alternative(onFailure,
					repository("quay.io/acme/foo", secretRef(quaySecret)), repository("ghcr.io/acme/foo", secretRef(ghcrSecret)))),
				"ImageAlternative/"+crName, "ghcr.io/acme/foo:1", Credential{Path: "ghcr.io/acme/foo", SecretRef: ghcrSecret}),
			Entry("a repositoryGroup entry the rewritten reference sits under",
				FromImageAlternative(alternative(onFailure,
					group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret)))),
				"ImageAlternative/"+crName, "ghcr.io/acme/foo:1", Credential{Path: ghcrGroup, SecretRef: ghcrSecret}),
			Entry("the most specific of two nested repositoryGroup entries",
				FromImageAlternative(alternative(onFailure,
					group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret)),
					group("ghcr.io/acme/team", secretRef("team")))),
				"ImageAlternative/"+crName, "ghcr.io/acme/team/foo:1", Credential{Path: "ghcr.io/acme/team", SecretRef: "team"}),
			Entry("an ImageMirror's destination.pull, the reference sitting under destination.path",
				FromImageMirror(mirror(onFailure, secretRef("mirror"))),
				"ImageMirror/"+crName, "registry.tld/mirror/quay.io/acme/foo:1_c", Credential{Path: "registry.tld/mirror", SecretRef: "mirror"}),
		)

		It("holds no entry for a rewritten reference no entry of the resource matches any more", func() {
			r := FromImageAlternative(alternative(onFailure, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret))))
			pod := rewrittenPod("ImageAlternative/"+crName, "docker.io/acme/foo:1", "docker.io/acme/foo:1")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(BeEmpty())
		})

		It("holds only the entries that served, not every injectable entry", func() {
			r := FromImageAlternative(alternative(onFailure,
				group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret)), group("docker.io/acme", secretRef(hubSecret))))
			pod := rewrittenPod("ImageAlternative/"+crName, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1")

			Expect(r.Needed(teamA, []*corev1.Pod{pod})).To(ConsistOf(Credential{Path: ghcrGroup, SecretRef: ghcrSecret}))
		})

		It("holds the entries of every live pod of the namespace, once each", func() {
			r := FromImageAlternative(alternative(onFailure,
				group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret)), group("docker.io/acme", secretRef(hubSecret))))
			by := "ImageAlternative/" + crName
			pods := []*corev1.Pod{
				rewrittenPod(by, "ghcr.io/acme/foo:1", "ghcr.io/acme/foo:1"),
				rewrittenPod(by, "ghcr.io/acme/bar:1", "ghcr.io/acme/bar:1"),
				rewrittenPod(by, "docker.io/acme/foo:1", "docker.io/acme/foo:1"),
			}

			Expect(r.Needed(teamA, pods)).To(ConsistOf(
				Credential{Path: ghcrGroup, SecretRef: ghcrSecret},
				Credential{Path: "docker.io/acme", SecretRef: hubSecret},
			))
		})
	})
})

var _ = Describe("Desired Secret", func() {
	var r Resource

	BeforeEach(func() {
		r = FromImageAlternative(alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, nil)))
	})

	It("is a kubernetes.io/dockerconfigjson Secret named after the resource identity", func() {
		secret, _ := Build(r, "team-a", nil, sourcesOf())

		Expect(*secret.Name).To(Equal(auth.InjectedSecretName("ImageAlternative", crName)))
		Expect(*secret.Namespace).To(Equal("team-a"))
		Expect(*secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
	})

	It("carries the managed-by label and an owner reference to the resource", func() {
		secret, _ := Build(r, "team-a", nil, sourcesOf())

		Expect(secret.Labels).To(HaveKeyWithValue(LabelManagedBy, ManagedBy))
		Expect(secret.OwnerReferences).To(HaveLen(1))
		owner := secret.OwnerReferences[0]
		Expect(*owner.APIVersion).To(Equal(kuikv1alpha1.GroupVersion.String()))
		Expect(*owner.Kind).To(Equal("ImageAlternative"))
		Expect(*owner.Name).To(Equal(crName))
		Expect(*owner.UID).To(Equal(r.UID))
	})

	DescribeTable("keys each auths entry by the registry path of its entry",
		func(r Resource, key string) {
			source := dockerConfig("source", map[string]string{quayHost: "q", "registry.tld": "m"})

			secret, failures := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(source))

			Expect(failures).To(BeEmpty())
			Expect(auths(secret)).To(HaveKey(key))
		},
		Entry("a repository verbatim",
			FromImageAlternative(alternative(always, repository("quay.io/acme/foo", secretRef("source")), repository("ghcr.io/acme/foo", nil))),
			"quay.io/acme/foo"),
		Entry("a repositoryGroup verbatim",
			FromImageAlternative(alternative(always, group(quayGroup, secretRef("source")), group(ghcrGroup, nil))),
			quayGroup),
		Entry("an ImageMirror destination.path without its trailing slash",
			FromImageMirror(mirror(always, secretRef("source"))),
			"registry.tld/mirror"),
	)

	It("keeps two entries of one host apart, each with its own credential", func() {
		r := FromImageAlternative(alternative(always, group(quayGroup, secretRef("acme")), group("quay.io/other", secretRef("other"))))
		sources := sourcesOf(
			dockerConfig("acme", map[string]string{quayHost: "acme-robot"}),
			dockerConfig("other", map[string]string{quayHost: "other-robot"}),
		)

		secret, _ := Build(r, "team-a", r.Needed(teamA, nil), sources)

		Expect(auths(secret)).To(Equal(map[string]string{quayGroup: "acme-robot", "quay.io/other": "other-robot"}))
	})

	It("fills an entry with the source credential the kubelet would pick for its path", func() {
		r := FromImageAlternative(alternative(always, repository("quay.io/acme/foo", secretRef("source")), repository("ghcr.io/acme/foo", nil)))
		source := dockerConfig("source", map[string]string{quayHost: "host", quayGroup: "project", "docker.io": hubSecret})

		secret, _ := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(source))

		Expect(auths(secret)).To(Equal(map[string]string{"quay.io/acme/foo": "project"}))
	})

	It("writes {\"auths\":{}} when no entry is left", func() {
		secret, _ := Build(r, "team-a", nil, sourcesOf())

		Expect(string(secret.Data[corev1.DockerConfigJsonKey])).To(Equal(`{"auths":{}}`))
	})

	Context("when a source credential cannot be resolved", func() {
		BeforeEach(func() {
			r = FromImageAlternative(alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, secretRef(ghcrSecret))))
		})

		It("leaves the entry out and writes the others", func() {
			secret, failures := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(dockerConfig(quaySecret, map[string]string{quayHost: "q"})))

			Expect(auths(secret)).To(Equal(map[string]string{quayGroup: "q"}))
			Expect(failures).To(ConsistOf(HaveField("Credential", Credential{Path: ghcrGroup, SecretRef: ghcrSecret})))
		})

		It("reports a secretRef missing from the install namespace with reason SecretNotFound", func() {
			_, failures := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(dockerConfig(quaySecret, map[string]string{quayHost: "q"})))

			Expect(failures).To(ConsistOf(HaveField("Reason", ReasonSecretNotFound)))
		})

		It("reports a secretRef that is not a docker-registry Secret", func() {
			opaque := dockerConfig(ghcrSecret, map[string]string{"ghcr.io": "g"})
			opaque.Type = corev1.SecretTypeOpaque

			_, failures := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(dockerConfig(quaySecret, map[string]string{quayHost: "q"}), opaque))

			Expect(failures).To(ConsistOf(HaveField("Reason", ReasonSecretMalformed)))
		})

		It("reports a secretRef holding no credential for the entry's path", func() {
			elsewhere := dockerConfig(ghcrSecret, map[string]string{"docker.io": hubSecret})

			_, failures := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(dockerConfig(quaySecret, map[string]string{quayHost: "q"}), elsewhere))

			Expect(failures).To(ConsistOf(HaveField("Reason", ReasonSecretMalformed)))
		})
	})

	It("never changes the cached resource or Secrets it is built from", func() {
		cr := alternative(always, group(quayGroup, secretRef(quaySecret)), group(ghcrGroup, nil))
		source := dockerConfig(quaySecret, map[string]string{quayHost: "q"})
		crBefore, sourceBefore := cr.DeepCopy(), source.DeepCopy()

		r := FromImageAlternative(cr)
		secret, _ := Build(r, "team-a", r.Needed(teamA, nil), sourcesOf(source))
		secret.Data[corev1.DockerConfigJsonKey][0] = 'x'
		secret.Labels["changed"] = "true"

		Expect(cr).To(Equal(crBefore))
		Expect(source).To(Equal(sourceBefore))
	})
})

var _ = Describe("Grace", func() {
	var (
		now   time.Time
		grace *Grace
		pair  = Pair{Namespace: "team-a"}
		a     = Credential{Path: quayGroup, SecretRef: quaySecret}
		b     = Credential{Path: ghcrGroup, SecretRef: ghcrSecret}
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
		grace = NewGrace(func() time.Time { return now })
	})

	It("keeps an entry that left the desired set for 15 minutes", func() {
		grace.Retain(pair, []Credential{a, b})
		now = now.Add(15*time.Minute - time.Second)

		Expect(grace.Retain(pair, []Credential{a})).To(ConsistOf(a, b))
	})

	It("drops it once 15 minutes have elapsed", func() {
		grace.Retain(pair, []Credential{a, b})
		now = now.Add(15 * time.Minute)

		Expect(grace.Retain(pair, []Credential{a})).To(ConsistOf(a))
	})

	It("restarts the period when the entry comes back and leaves again", func() {
		grace.Retain(pair, []Credential{a, b})
		now = now.Add(10 * time.Minute)
		grace.Retain(pair, []Credential{a})
		now = now.Add(2 * time.Minute)
		grace.Retain(pair, []Credential{a, b})

		now = now.Add(14 * time.Minute)
		Expect(grace.Retain(pair, []Credential{a})).To(ConsistOf(a, b))
		now = now.Add(time.Minute)
		Expect(grace.Retain(pair, []Credential{a})).To(ConsistOf(a))
	})

	It("tracks each pair and entry separately", func() {
		other := Pair{Namespace: "team-b"}
		grace.Retain(pair, []Credential{a, b})
		grace.Retain(other, []Credential{a})
		now = now.Add(10 * time.Minute)
		grace.Retain(pair, []Credential{b})
		grace.Retain(other, []Credential{a})

		now = now.Add(6 * time.Minute)
		Expect(grace.Retain(pair, nil)).To(ConsistOf(b))
		Expect(grace.Retain(other, nil)).To(ConsistOf(a))
	})
})
