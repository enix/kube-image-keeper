package synth

import (
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	repo      = "acme/foo"
	archAMD64 = "amd64"
	osLinux   = "linux"
)

func indexDigest(key string, gen int) v1.Hash {
	GinkgoHelper()
	idx, err := Build(key, gen, nil)
	Expect(err).NotTo(HaveOccurred())
	d, err := idx.Digest()
	Expect(err).NotTo(HaveOccurred())
	return d
}

var _ = Describe("Build", func() {
	It("is deterministic", func() {
		Expect(indexDigest(repo, 1)).To(Equal(indexDigest(repo, 1)), "digest changed across builds")
	})

	It("builds one platform per sleep binary, linux/amd64 first", func() {
		names := make([]string, 0, len(Platforms))
		for _, p := range Platforms {
			names = append(names, PlatformName(p))
		}
		Expect(names).To(Equal([]string{"linux/amd64", "linux/arm64"}))
	})

	It("differs by key and generation while sharing the layers", func() {
		seen := map[v1.Hash]bool{}
		for _, c := range []struct {
			key string
			gen int
		}{{repo, 1}, {repo, 2}, {"acme/bar", 1}} {
			d := indexDigest(c.key, c.gen)
			Expect(seen).NotTo(HaveKey(d), "duplicate digest %s for %v", d, c)
			seen[d] = true
			idx, _ := Build(c.key, c.gen, nil)
			im, _ := idx.IndexManifest()
			for _, m := range im.Manifests {
				img, err := idx.Image(m.Digest)
				Expect(err).NotTo(HaveOccurred())
				layers, _ := img.Layers()
				got, _ := layers[0].Digest()
				l, err := Layer(m.Platform.Architecture)
				Expect(err).NotTo(HaveOccurred())
				want, _ := l.Digest()
				Expect(got).To(Equal(want), "%v %s: layer %s, want shared %s", c, m.Platform.Architecture, got, want)
			}
		}
	})

	It("builds an OCI index of two linux images", func() {
		idx, err := Build(repo, 3, nil)
		Expect(err).NotTo(HaveOccurred())
		mt, _ := idx.MediaType()
		Expect(mt).To(Equal(types.OCIImageIndex))
		im, _ := idx.IndexManifest()
		Expect(im.Manifests).To(HaveLen(2))
		for i, want := range []string{archAMD64, "arm64"} {
			m := im.Manifests[i]
			Expect(m.Platform).NotTo(BeNil(), "manifest %d platform", i)
			Expect(m.Platform.OS).To(Equal(osLinux), "manifest %d platform %+v", i, m.Platform)
			Expect(m.Platform.Architecture).To(Equal(want), "manifest %d platform %+v", i, m.Platform)
			Expect(m.MediaType).To(Equal(types.OCIManifestSchema1), "manifest %d media type", i)
			img, _ := idx.Image(m.Digest)
			cf, err := img.ConfigFile()
			Expect(err).NotTo(HaveOccurred())
			Expect(cf.Config.Entrypoint).To(Equal([]string{"/sleep"}))
			Expect(cf.Config.Labels).To(HaveKeyWithValue(labelContent, repo))
			Expect(cf.Config.Labels).To(HaveKeyWithValue(labelGeneration, "3"))
			Expect(cf.OS).To(Equal(osLinux))
			Expect(cf.Architecture).To(Equal(want))
			layers, _ := img.Layers()
			Expect(layers).To(HaveLen(1))
			d, _ := layers[0].Digest()
			diff, _ := layers[0].DiffID()
			Expect(d).To(Equal(diff), "layer digest != diffID")
		}
	})

	It("refuses a platform without a sleep binary", func() {
		_, err := Build(repo, 1, []v1.Platform{{OS: osLinux, Architecture: "riscv64"}})
		Expect(err).To(MatchError(ContainSubstring("riscv64")))
	})

	It("builds a single-platform index on request", func() {
		one := []v1.Platform{{OS: osLinux, Architecture: archAMD64}}
		idx, err := Build(repo, 1, one)
		Expect(err).NotTo(HaveOccurred())
		im, _ := idx.IndexManifest()
		Expect(im.Manifests).To(HaveLen(1), "want one amd64")
		Expect(im.Manifests[0].Platform.Architecture).To(Equal(archAMD64))
		d, _ := idx.Digest()
		both, _ := Build(repo, 1, nil)
		bd, _ := both.Digest()
		Expect(d).NotTo(Equal(bd), "single-platform digest equals the two-platform digest")
	})
})
