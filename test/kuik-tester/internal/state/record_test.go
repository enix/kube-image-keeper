package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
)

var _ = Describe("RecordImage on a failed write", func() {
	const newKey = "acme/new"
	var idx v1.ImageIndex

	BeforeEach(func() {
		var err error
		idx, err = synth.Build(newKey, 1, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("publishes a recorded image even when the state file cannot be written", func() {
		p := filepath.Join(GinkgoT().TempDir(), "state.json")
		st, err := Open(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Update(func(*State) error { return nil })).To(Succeed())
		before, err := os.ReadFile(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Mkdir(p+".tmp", 0o700)).To(Succeed())

		Expect(st.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		d, err := idx.Digest()
		Expect(err).NotTo(HaveOccurred())
		s := st.Snapshot()
		Expect(s.Seen).To(HaveKey(RefKey("upstream", newKey, "t1")))
		Expect(s.Digests).To(HaveKey(d.String()))
		after, err := os.ReadFile(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(after)).To(Equal(string(before)), "state file")

		Expect(st.Update(addRule("r1"))).NotTo(Succeed())
		s = st.Snapshot()
		Expect(s.Seen).To(HaveKey(RefKey("upstream", newKey, "t1")))
		Expect(s.Digests).To(HaveKey(d.String()))
		Expect(s.Rules).To(BeEmpty())
	})
})

var _ = Describe("RecordImage bounds", func() {
	const newKey = "acme/new"
	t0 := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	var (
		store *Store
		idx   v1.ImageIndex
	)

	BeforeEach(func() {
		var err error
		store, err = Open("")
		Expect(err).NotTo(HaveOccurred())
		idx, err = synth.Build(newKey, 1, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("forgets the oldest seen image when the seen set is full", func() {
		Expect(store.Update(func(s *State) error {
			for i := range maxSeen {
				s.Seen[fmt.Sprintf("upstream/acme/old-%d:t", i)] = t0.Add(time.Duration(i) * time.Second)
			}
			return nil
		})).To(Succeed())
		Expect(store.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		store.View(func(s *State) {
			Expect(s.Seen).To(HaveLen(maxSeen), "seen images")
			Expect(s.Seen).NotTo(HaveKey("upstream/acme/old-0:t"), "oldest seen image")
			Expect(s.Seen).To(HaveKey("upstream/acme/old-1:t"), "second oldest seen image")
			Expect(s.Seen).To(HaveKey(RefKey("upstream", newKey, "t1")), "new image")
		})
	})

	It("evicts the smallest key among seen images of the same time", func() {
		same := time.Now().Add(-time.Hour)
		Expect(store.Update(func(s *State) error {
			for i := range maxSeen {
				s.Seen[fmt.Sprintf("upstream/acme/old-%05d:t", i)] = same
			}
			return nil
		})).To(Succeed())
		Expect(store.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		store.View(func(s *State) {
			Expect(s.Seen).To(HaveLen(maxSeen), "seen images")
			Expect(s.Seen).NotTo(HaveKey("upstream/acme/old-00000:t"), "smallest key")
			Expect(s.Seen).To(HaveKey("upstream/acme/old-00001:t"), "second smallest key")
			Expect(s.Seen).To(HaveKey(RefKey("upstream", newKey, "t1")), "new image")
		})
	})

	It("keeps a new image pullable by digest when the digest index is full", func() {
		Expect(store.Update(func(s *State) error {
			for i := range maxDigests {
				s.Digests[fmt.Sprintf("sha256:%064x", i)] = DigestEntry{ContentKey: "acme/filler", Generation: 1, Kind: DigestIndex}
			}
			return nil
		})).To(Succeed())
		Expect(store.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		d, err := idx.Digest()
		Expect(err).NotTo(HaveOccurred())
		im, err := idx.IndexManifest()
		Expect(err).NotTo(HaveOccurred())
		m := im.Manifests[0].Digest
		img, err := idx.Image(m)
		Expect(err).NotTo(HaveOccurred())
		cn, err := img.ConfigName()
		Expect(err).NotTo(HaveOccurred())
		store.View(func(s *State) {
			Expect(s.Digests).To(HaveLen(maxDigests), "digests")
			Expect(s.Digests).To(HaveKey(d.String()), "index digest")
			Expect(s.Digests).To(HaveKey(m.String()), "platform manifest digest")
			Expect(s.Digests).To(HaveKey(cn.String()), "config digest")
		})
	})

	It("records the digests but no seen image for an empty tag", func() {
		Expect(store.RecordImage("upstream", newKey, "", newKey, 1, idx)).To(Succeed())
		d, err := idx.Digest()
		Expect(err).NotTo(HaveOccurred())
		store.View(func(s *State) {
			Expect(s.Seen).To(BeEmpty())
			Expect(s.Digests).To(HaveKey(d.String()), "index digest")
		})
	})

	It("does not rewrite the state for an image already recorded", func() {
		key := RefKey("upstream", newKey, "t1")
		Expect(store.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		var first time.Time
		store.View(func(s *State) { first = s.Seen[key] })
		Expect(first).NotTo(BeZero(), "seen after the first record")
		Expect(store.RecordImage("upstream", newKey, "t1", newKey, 1, idx)).To(Succeed())
		store.View(func(s *State) { Expect(s.Seen[key]).To(Equal(first), "seen time after the second record") })
	})
})
