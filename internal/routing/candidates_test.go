package routing

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
)

var _ = Describe("Candidates", func() {
	Context("selecting the resources", func() {
		It("applies a resource with neither podSelector nor namespaceSelector to every pod", func() {
			index := NewIndex(alternatives(library(onFailure)), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(ContainElement(ecrNginx))
		})

		It("skips a resource whose podSelector does not match the labels of the pod", func() {
			cr := library(onFailure)
			cr.Spec.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx}))
		})

		It("skips a resource whose namespaceSelector does not match the labels of the pod's namespace", func() {
			cr := library(onFailure)
			cr.Spec.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"team": "b"}}
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx}))
		})

		It("offers nothing but the original when no resource applies", func() {
			result := NewIndex(nil, nil).Candidates(request(nginx), options)
			Expect(references(result)).To(Equal([]string{dockerNginx}))
			Expect(result.Offering).To(BeEmpty())
		})
	})

	Context("matching an ImageAlternative", func() {
		It("offers the other entries of a repository list with the tag of the image", func() {
			cr := alternative("acme", onFailure, repository("quay.io/acme/foo"), repository("docker.io/acme-org/foo"))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request("quay.io/acme/foo:latest"), options))).
				To(Equal([]string{"quay.io/acme/foo:latest", "docker.io/acme-org/foo:latest"}))
		})

		It("offers the other entries of a repositoryGroup list with the remainder below the group", func() {
			cr := alternative("acme", onFailure, group("quay.io/acme/foo"), group("docker.io/acme-org/foo"))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request("quay.io/acme/foo/bar/oni:latest"), options))).
				To(Equal([]string{"quay.io/acme/foo/bar/oni:latest", "docker.io/acme-org/foo/bar/oni:latest"}))
		})

		It("matches the image once normalised", func() {
			index := NewIndex(alternatives(library(onFailure)), nil)
			Expect(references(index.Candidates(request("nginx"), options))).To(ContainElement("public.ecr.aws/docker/library/nginx:latest"))
		})

		It("carries the remainder of the most specific entry when several entries of one resource match", func() {
			cr := alternative("acme", onFailure, group("quay.io/acme/foo"), group("quay.io/acme"), group("ghcr.io/mirror"))
			index := NewIndex(alternatives(cr), nil)
			refs := references(index.Candidates(request("quay.io/acme/foo/bar:v1"), options))
			Expect(refs).To(ContainElement("ghcr.io/mirror/bar:v1"))
			Expect(refs).NotTo(ContainElement("ghcr.io/mirror/foo/bar:v1"))
		})

		It("does not apply a resource none of whose entries matches the image", func() {
			cr := alternative("acme", onFailure, repository("quay.io/acme/foo"), repository("docker.io/acme-org/foo"))
			result := NewIndex(alternatives(cr), nil).Candidates(request("quay.io/acme/foo-bar:latest"), options)
			Expect(references(result)).To(Equal([]string{"quay.io/acme/foo-bar:latest"}))
			Expect(result.Offering).To(BeEmpty())
		})

		It("merges the entries of overlapping resources instead of letting one shadow the other", func() {
			broad := alternative("broad", onFailure, group("quay.io/acme"), group("ghcr.io/acme"))
			narrow := alternative("narrow", onFailure, repository("quay.io/acme/foo"), repository("docker.io/acme/foo"))
			index := NewIndex(alternatives(broad, narrow), nil)
			Expect(references(index.Candidates(request("quay.io/acme/foo:v1"), options))).
				To(ContainElements("ghcr.io/acme/foo:v1", "docker.io/acme/foo:v1"))
		})
	})

	Context("ordering", func() {
		// docs/v3/spec.md, "Candidate ordering": mirror M, alternatives ecr, gcr and docker.io,
		// the pod's original being docker.io.
		DescribeTable("merges the bands of both kinds around the original",
			func(mirrorPolicy, alternativePolicy string, want []string) {
				index := NewIndex(alternatives(library(alternativePolicy)), mirrors(prodMirror(mirrorPolicy)))
				Expect(references(index.Candidates(request(nginx), options))).To(Equal(tableReferences(want)))
			},
			Entry("OnFailure mirror, OnFailure alternative", onFailure, onFailure, []string{dockerIO, ecr, gcr, mirror}),
			Entry("OnFailure mirror, Always alternative", onFailure, always, []string{ecr, gcr, dockerIO, mirror}),
			Entry("Always mirror, OnFailure alternative", always, onFailure, []string{mirror, dockerIO, ecr, gcr}),
			Entry("Always mirror, Always alternative", always, always, []string{mirror, ecr, gcr, dockerIO}),
		)

		It("puts the original once, at the pivot, whatever its position in alternatives", func() {
			cr := alternative("library", always, group(dockerLibrary), group(ecrLibrary), group(gcrLibrary))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{ecrNginx, gcrNginx, dockerNginx}))
		})

		It("keeps the declared order of a resource's entries within its band", func() {
			cr := alternative("library", onFailure, group(gcrLibrary), group(ecrLibrary), group(dockerLibrary))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx, gcrNginx, ecrNginx}))
		})

		It("sorts the resources of one kind and one policy by name", func() {
			b := alternative("b", onFailure, group(dockerLibrary), group("quay.io/b"))
			a := alternative("a", onFailure, group(dockerLibrary), group("quay.io/a"))
			index := NewIndex(alternatives(b, a), nil)
			Expect(references(index.Candidates(request(nginx), options))).
				To(Equal([]string{dockerNginx, "quay.io/a/nginx:1.27", "quay.io/b/nginx:1.27"}))
		})
	})

	Context("with an entry marked unavailable", func() {
		It("applies a resource whose only matching entry is marked unavailable", func() {
			cr := alternative("library", onFailure, unavailable(group(dockerLibrary)), group(ecrLibrary))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(ContainElement(ecrNginx))
		})

		It("offers no candidate for an entry marked unavailable", func() {
			cr := alternative("library", onFailure, group(dockerLibrary), unavailable(group(ecrLibrary)), group(gcrLibrary))
			index := NewIndex(alternatives(cr), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx, gcrNginx}))
		})

		It("demotes the original to the end of the list when it matches an entry marked unavailable", func() {
			cr := alternative("library", onFailure, unavailable(group(dockerLibrary)), group(ecrLibrary), group(gcrLibrary))
			index := NewIndex(alternatives(cr), mirrors(prodMirror(onFailure)))
			Expect(references(index.Candidates(request(nginx), options))).
				To(Equal([]string{ecrNginx, gcrNginx, mirrorNginx, dockerNginx}))
		})
	})

	Context("with an ImageMirror", func() {
		It("offers one candidate, the destination reference of the original", func() {
			index := NewIndex(nil, mirrors(prodMirror(onFailure)))
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx, mirrorNginx}))
		})

		It("offers none under rewritePolicy None", func() {
			index := NewIndex(nil, mirrors(prodMirror(string(kuikv1alpha1.RewritePolicyNone))))
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx}))
		})

		It("offers none for an image matching excludeImages", func() {
			m := prodMirror(onFailure)
			m.Spec.ExcludeImages = []string{"docker.io/library/**"}
			index := NewIndex(nil, mirrors(m))
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx}))
		})

		It("offers none for an image already under its own destination.path", func() {
			index := NewIndex(nil, mirrors(prodMirror(onFailure)))
			Expect(references(index.Candidates(request(mirrorNginx), options))).To(Equal([]string{mirrorNginx}))
		})
	})

	Context("for a container with imagePullPolicy Always", func() {
		It("moves an Always ImageMirror candidate to the OnFailure mirror band", func() {
			index := NewIndex(alternatives(library(onFailure)), mirrors(prodMirror(always)))
			Expect(references(index.Candidates(pullAlways(request(nginx)), options))).
				To(Equal([]string{dockerNginx, ecrNginx, gcrNginx, mirrorNginx}))
		})

		It("leaves an Always ImageAlternative in its band", func() {
			index := NewIndex(alternatives(library(always)), nil)
			Expect(references(index.Candidates(pullAlways(request(nginx)), options))).
				To(Equal([]string{ecrNginx, gcrNginx, dockerNginx}))
		})

		It("keeps an Always ImageMirror in its band when demoteMirrorWithPullPolicyAlways is false", func() {
			index := NewIndex(alternatives(library(onFailure)), mirrors(prodMirror(always)))
			opts := options
			opts.DemoteMirrorWithPullPolicyAlways = false
			Expect(references(index.Candidates(pullAlways(request(nginx)), opts))).
				To(Equal([]string{mirrorNginx, dockerNginx, ecrNginx, gcrNginx}))
		})
	})

	Context("the config each candidate is probed with", func() {
		It("carries the auth and insecure of the alternative entry that produced it", func() {
			entry := group(ecrLibrary)
			entry.Auth = secretAuth("ecr-creds")
			entry.Insecure = true
			index := NewIndex(alternatives(alternative("library", onFailure, group(dockerLibrary), entry)), nil)
			Expect(candidate(index.Candidates(request(nginx), options), ecrNginx).Config).
				To(Equal(Config{Auth: secretAuth("ecr-creds"), Insecure: true}))
		})

		It("carries destination.pull and destination.insecure for a mirror candidate", func() {
			m := prodMirror(onFailure)
			m.Spec.Destination.Pull = &kuikv1alpha1.DestinationCredentials{Auth: *secretAuth("mirror-read")}
			m.Spec.Destination.Insecure = true
			index := NewIndex(nil, mirrors(m))
			Expect(candidate(index.Candidates(request(nginx), options), mirrorNginx).Config).
				To(Equal(Config{Auth: secretAuth("mirror-read"), Insecure: true}))
		})

		// The kubelet pulls the original with no entry credential: the pod keeps no record, so
		// nothing is injected for it.
		It("carries no entry auth for the original", func() {
			entry := group(dockerLibrary)
			entry.Auth = secretAuth("dockerhub-creds")
			index := NewIndex(alternatives(alternative("library", onFailure, entry, group(ecrLibrary))), nil)
			Expect(candidate(index.Candidates(request(nginx), options), dockerNginx).Config.Auth).To(BeNil())
		})
	})

	Context("deduplication", func() {
		It("keeps the first occurrence of a reference offered twice with the same config", func() {
			a := alternative("a", onFailure, group(dockerLibrary), group(ecrLibrary))
			b := alternative("b", onFailure, group(dockerLibrary), group(ecrLibrary))
			result := NewIndex(alternatives(b, a), nil).Candidates(request(nginx), options)
			Expect(references(result)).To(Equal([]string{dockerNginx, ecrNginx}))
			Expect(candidate(result, ecrNginx).Resource).To(Equal(&Resource{Kind: KindImageAlternative, Name: "a"}))
		})

		It("keeps both occurrences of a reference offered with different configs", func() {
			authenticated := group(ecrLibrary)
			authenticated.Auth = secretAuth("ecr-creds")
			a := alternative("a", onFailure, group(dockerLibrary), group(ecrLibrary))
			b := alternative("b", onFailure, group(dockerLibrary), authenticated)
			index := NewIndex(alternatives(a, b), nil)
			Expect(references(index.Candidates(request(nginx), options))).To(Equal([]string{dockerNginx, ecrNginx, ecrNginx}))
		})
	})

	Context("attribution", func() {
		It("attributes each candidate to the resource that offered it and to the policy of its band", func() {
			result := NewIndex(alternatives(library(always)), mirrors(prodMirror(onFailure))).Candidates(request(nginx), options)
			Expect(candidate(result, ecrNginx).Resource).To(Equal(&Resource{Kind: KindImageAlternative, Name: "library"}))
			Expect(candidate(result, ecrNginx).Policy).To(Equal(kuikv1alpha1.RewritePolicyAlways))
			Expect(candidate(result, mirrorNginx).Resource).To(Equal(&Resource{Kind: KindImageMirror, Name: prodMirrorName}))
			Expect(candidate(result, mirrorNginx).Policy).To(Equal(kuikv1alpha1.RewritePolicyOnFailure))
			Expect(candidate(result, dockerNginx).Resource).To(BeNil())
		})

		It("attributes a demoted Always ImageMirror candidate the OnFailure policy", func() {
			result := NewIndex(nil, mirrors(prodMirror(always))).Candidates(pullAlways(request(nginx)), options)
			Expect(candidate(result, mirrorNginx).Policy).To(Equal(kuikv1alpha1.RewritePolicyOnFailure))
		})

		It("lists every resource that offered a candidate", func() {
			archive := prodMirror(string(kuikv1alpha1.RewritePolicyNone))
			archive.Name = "archive"
			index := NewIndex(alternatives(library(onFailure)), mirrors(prodMirror(onFailure), archive))
			Expect(index.Candidates(request(nginx), options).Offering).To(ConsistOf(
				Resource{Kind: KindImageAlternative, Name: "library"},
				Resource{Kind: KindImageMirror, Name: prodMirrorName},
			))
		})
	})
})

// Fixtures of the ordering table: the rewrite policies and the candidates, named as the
// spec's table names them.
const (
	always    = "Always"
	onFailure = "OnFailure"
	dockerIO  = "docker.io"
	ecr       = "ecr"
	gcr       = "gcr"
	mirror    = "M"
)

// The repository groups of walkthrough 01, the image every spec routes by default, and the
// references it is routed to.
const (
	dockerLibrary = "docker.io/library"
	ecrLibrary    = "public.ecr.aws/docker/library"
	gcrLibrary    = "mirror.gcr.io/library"

	nginx       = "nginx:1.27"
	dockerNginx = "docker.io/library/nginx:1.27"
	ecrNginx    = "public.ecr.aws/docker/library/nginx:1.27"
	gcrNginx    = "mirror.gcr.io/library/nginx:1.27"
	mirrorNginx = "registry.tld/mirror/docker.io/library/nginx:1.27_cluster-a"

	prodMirrorName = "prod-mirror"
)

// options are the global config settings every spec uses, the defaults of the spec.
var options = Options{ClusterID: "cluster-a", DemoteMirrorWithPullPolicyAlways: true}

// tableReferences maps the names of the ordering table to their references.
func tableReferences(names []string) []string {
	byName := map[string]string{dockerIO: dockerNginx, ecr: ecrNginx, gcr: gcrNginx, mirror: mirrorNginx}
	refs := make([]string, 0, len(names))
	for _, n := range names {
		refs = append(refs, byName[n])
	}
	return refs
}

// request is a container of image with imagePullPolicy IfNotPresent, in a labelled pod and
// namespace.
func request(image string) Request {
	GinkgoHelper()
	ref, err := imagepath.Parse(image)
	Expect(err).NotTo(HaveOccurred())
	return Request{
		Image:           ref,
		PullPolicy:      corev1.PullIfNotPresent,
		PodLabels:       map[string]string{"app": "web"},
		NamespaceLabels: map[string]string{"team": "a"},
	}
}

// pullAlways sets imagePullPolicy Always on req.
func pullAlways(req Request) Request {
	req.PullPolicy = corev1.PullAlways
	return req
}

// library is walkthrough 01's ImageAlternative under policy: ecr, gcr, then docker.io.
func library(policy string) kuikv1alpha1.ImageAlternative {
	return alternative("library", policy, group(ecrLibrary), group(gcrLibrary), group(dockerLibrary))
}

func alternative(name, policy string, entries ...kuikv1alpha1.Alternative) kuikv1alpha1.ImageAlternative {
	return kuikv1alpha1.ImageAlternative{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kuikv1alpha1.ImageAlternativeSpec{
			RewritePolicy: kuikv1alpha1.RewritePolicy(policy),
			Alternatives:  entries,
		},
	}
}

// prodMirror is an ImageMirror under policy with destination registry.tld/mirror/.
func prodMirror(policy string) kuikv1alpha1.ImageMirror {
	return kuikv1alpha1.ImageMirror{
		ObjectMeta: metav1.ObjectMeta{Name: prodMirrorName},
		Spec: kuikv1alpha1.ImageMirrorSpec{
			RewritePolicy: kuikv1alpha1.RewritePolicy(policy),
			Destination:   kuikv1alpha1.MirrorDestination{Path: "registry.tld/mirror/"},
		},
	}
}

func alternatives(crs ...kuikv1alpha1.ImageAlternative) []kuikv1alpha1.ImageAlternative { return crs }

func mirrors(crs ...kuikv1alpha1.ImageMirror) []kuikv1alpha1.ImageMirror { return crs }

func repository(path string) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{Repository: path}
}

func group(path string) kuikv1alpha1.Alternative {
	return kuikv1alpha1.Alternative{RepositoryGroup: path}
}

func unavailable(entry kuikv1alpha1.Alternative) kuikv1alpha1.Alternative {
	entry.Unavailable = true
	return entry
}

func secretAuth(name string) *kuikv1alpha1.Auth {
	return &kuikv1alpha1.Auth{SecretRef: &kuikv1alpha1.SecretReference{Name: name}}
}

// references lists the references of a candidate list, in order.
func references(result Result) []string {
	refs := make([]string, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		refs = append(refs, c.Reference)
	}
	return refs
}

// candidate returns the first candidate of result with reference ref.
func candidate(result Result, ref string) Candidate {
	GinkgoHelper()
	for _, c := range result.Candidates {
		if c.Reference == ref {
			return c
		}
	}
	Fail("no candidate " + ref)
	return Candidate{}
}
