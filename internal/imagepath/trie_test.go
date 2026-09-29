package imagepath

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// Fixtures: a repository path of the spec's tables, and the names of trie entries.
const (
	acmeFoo        = "quay.io/acme/foo"
	nameFoo        = "foo"
	nameAcme       = "acme"
	nameHub        = "hub"
	nameRepository = "repository"
)

// entry is a value inserted in the trie: the CR that declared it and a label for the entry.
type entry struct {
	owner string
	name  string
}

func mustParse(s string) Reference {
	GinkgoHelper()
	ref, err := Parse(s)
	Expect(err).NotTo(HaveOccurred())
	return ref
}

func mustPath(s string, kind Kind) Path {
	GinkgoHelper()
	p, err := ParsePath(s, kind)
	Expect(err).NotTo(HaveOccurred())
	return p
}

// names lists the entries of the matches, in order.
func names(matches []Match[entry]) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Value.name)
	}
	return out
}

// rewriteFirst rewrites image with its first match to target, or returns "" without a match.
func rewriteFirst(t *Trie[entry], image string, target Path) string {
	GinkgoHelper()
	ref := mustParse(image)
	matches := t.Match(ref)
	if len(matches) == 0 {
		return ""
	}
	return Rewrite(ref, matches[0], target).String()
}

var _ = Describe("Trie", func() {
	// The tables of docs/v3/spec.md, "repository" and "repositoryGroup": the entry
	// quay.io/acme/foo rewritten to docker.io/acme-org/foo. An empty result is no match.
	DescribeTable("a repository entry",
		func(image, rewrittenTo string) {
			t := NewTrie[entry]()
			t.Insert(mustPath(acmeFoo, Repository), Repository, entry{name: nameFoo})
			Expect(rewriteFirst(t, image, mustPath("docker.io/acme-org/foo", Repository))).To(Equal(rewrittenTo))
		},
		Entry("matches the exact repository and keeps the tag", "quay.io/acme/foo:latest", "docker.io/acme-org/foo:latest"),
		Entry("does not match a repository sharing a prefix inside a segment", "quay.io/acme/foo-bar:latest", ""),
		Entry("does not match a repository deeper than the entry", "quay.io/acme/foo/bar:latest", ""),
		Entry("does not match a repository two segments deeper", "quay.io/acme/foo/bar/oni:latest", ""),
	)

	DescribeTable("a repositoryGroup entry",
		func(image, rewrittenTo string) {
			t := NewTrie[entry]()
			t.Insert(mustPath(acmeFoo, Group), Group, entry{name: nameFoo})
			Expect(rewriteFirst(t, image, mustPath("docker.io/acme-org/foo", Group))).To(Equal(rewrittenTo))
		},
		Entry("does not match the group itself", "quay.io/acme/foo:latest", ""),
		Entry("does not match a repository sharing a prefix inside a segment", "quay.io/acme/foo-bar:latest", ""),
		Entry("matches one segment below and carries it over", "quay.io/acme/foo/bar:latest", "docker.io/acme-org/foo/bar:latest"),
		Entry("matches at any depth and carries every segment over", "quay.io/acme/foo/bar/oni:latest", "docker.io/acme-org/foo/bar/oni:latest"),
	)

	It("matches a short image name against a docker.io repository written without library", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("docker.io/nginx", Repository), Repository, entry{name: "nginx"})
		Expect(names(t.Match(mustParse("nginx:1.27")))).To(Equal([]string{"nginx"}))
	})

	It("matches a short image name under a docker.io group, whose path gets no library segment", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("docker.io/bitnami", Group), Group, entry{name: "bitnami"})
		matches := t.Match(mustParse("bitnami/redis:7"))
		Expect(names(matches)).To(Equal([]string{"bitnami"}))
		Expect(matches[0].Remainder).To(Equal([]string{"redis"}))
	})

	It("keeps the digest on rewrite", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{name: nameAcme})
		Expect(rewriteFirst(t, "quay.io/acme/foo@sha256:"+digestHex, mustPath("docker.io/acme-org", Group))).
			To(Equal("docker.io/acme-org/foo@sha256:" + digestHex))
	})

	It("matches every repository of a host with a group declared on the host alone", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("docker.io", Group), Group, entry{name: nameHub})
		Expect(names(t.Match(mustParse("nginx")))).To(Equal([]string{nameHub}))
		Expect(names(t.Match(mustParse("bitnami/redis/sub:1")))).To(Equal([]string{nameHub}))
		Expect(t.Match(mustParse(acmeFoo))).To(BeEmpty())
	})

	It("does not match the same path on the host without its port", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("registry.local:5000/mirror", Group), Group, entry{name: "mirror"})
		Expect(names(t.Match(mustParse("registry.local:5000/mirror/foo:v1")))).To(Equal([]string{"mirror"}))
		Expect(t.Match(mustParse("registry.local/mirror/foo:v1"))).To(BeEmpty())
	})

	It("holds a repository and a group on the same path, each matching its own images", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath(acmeFoo, Repository), Repository, entry{name: nameRepository})
		t.Insert(mustPath(acmeFoo, Group), Group, entry{name: "group"})
		Expect(names(t.Match(mustParse("quay.io/acme/foo:v1")))).To(Equal([]string{nameRepository}))
		Expect(names(t.Match(mustParse("quay.io/acme/foo/bar:v1")))).To(Equal([]string{"group"}))
	})

	It("returns every value declared on the same path", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{owner: "a", name: "a"})
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{owner: "b", name: "b"})
		Expect(names(t.Match(mustParse("quay.io/acme/foo:v1")))).To(ConsistOf("a", "b"))
	})

	It("returns a repository first, then the groups from the deepest to the shallowest", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("quay.io", Group), Group, entry{name: "host"})
		t.Insert(mustPath(acmeFoo, Group), Group, entry{name: nameFoo})
		t.Insert(mustPath("quay.io/acme/foo/bar", Repository), Repository, entry{name: nameRepository})
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{name: nameAcme})
		matches := t.Match(mustParse("quay.io/acme/foo/bar:v1"))
		Expect(names(matches)).To(Equal([]string{nameRepository, nameFoo, nameAcme, "host"}))
		Expect(matches[0].Kind).To(Equal(Repository))
		Expect(matches[1].Depth).To(BeNumerically(">", matches[2].Depth))
	})
})

var _ = Describe("MostSpecificPerOwner", func() {
	owner := func(e entry) string { return e.owner }

	It("keeps the deepest of two entries of one owner, with its remainder", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{owner: "cr", name: nameAcme})
		t.Insert(mustPath(acmeFoo, Group), Group, entry{owner: "cr", name: nameFoo})
		kept := MostSpecificPerOwner(t.Match(mustParse("quay.io/acme/foo/bar/oni:v1")), owner)
		Expect(names(kept)).To(Equal([]string{nameFoo}))
		Expect(kept[0].Remainder).To(Equal([]string{"bar", "oni"}))
	})

	It("keeps one match per owner", func() {
		t := NewTrie[entry]()
		t.Insert(mustPath("quay.io/acme", Group), Group, entry{owner: "broad", name: "broad-acme"})
		t.Insert(mustPath("quay.io", Group), Group, entry{owner: "broad", name: "broad-host"})
		t.Insert(mustPath(acmeFoo, Repository), Repository, entry{owner: "narrow", name: "narrow-foo"})
		kept := MostSpecificPerOwner(t.Match(mustParse("quay.io/acme/foo:v1")), owner)
		Expect(names(kept)).To(ConsistOf("narrow-foo", "broad-acme"))
	})
})

var _ = Describe("ValidateAlternatives", func() {
	It("accepts a list of repository entries", func() {
		Expect(ValidateAlternatives([]kuikv1alpha1.Alternative{
			{Repository: acmeFoo},
			{Repository: "docker.io/acme-org/foo"},
		})).To(Succeed())
	})

	DescribeTable("rejects",
		func(alternatives []kuikv1alpha1.Alternative) {
			Expect(ValidateAlternatives(alternatives)).NotTo(Succeed())
		},
		Entry("an entry carrying both repository and repositoryGroup", []kuikv1alpha1.Alternative{
			{Repository: acmeFoo, RepositoryGroup: "quay.io/acme"},
		}),
		Entry("an entry carrying neither", []kuikv1alpha1.Alternative{
			{Repository: acmeFoo}, {Insecure: true},
		}),
		Entry("an entry whose path is invalid", []kuikv1alpha1.Alternative{
			{Repository: acmeFoo}, {Repository: "quay.io/acme/foo:v1"},
		}),
		Entry("a list mixing repository and repositoryGroup entries", []kuikv1alpha1.Alternative{
			{Repository: acmeFoo}, {RepositoryGroup: "docker.io/acme-org/foo"},
		}),
	)
})
