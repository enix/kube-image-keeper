package state

import (
	"os"
	"path/filepath"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
)

const (
	expireKey  = "acme/exp"
	expireRef  = "upstream/acme/exp:t1"
	expireHour = time.Hour
)

var _ = Describe("Expire", func() {
	var (
		now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		ttl = 24 * time.Hour
		st  *Store
	)

	BeforeEach(func() {
		var err error
		st, err = Open(filepath.Join(GinkgoT().TempDir(), "state.json"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("drops an old image with its generation and digests, and keeps the recent ones", func() {
		Expect(st.Update(func(s *State) error {
			s.Seen["h/old:t"] = now.Add(-ttl - expireHour)
			s.Generations["h/old:t"] = 3
			s.Seen["h/new:t"] = now.Add(-expireHour)
			s.Generations["h/new:t"] = 2
			s.Digests["sha256:old"] = DigestEntry{LastSeen: now.Add(-ttl - expireHour)}
			s.Digests["sha256:new"] = DigestEntry{LastSeen: now.Add(-expireHour)}
			return nil
		})).To(Succeed())
		images, digests, err := st.Expire(now, ttl)
		Expect(err).NotTo(HaveOccurred())
		Expect(images).To(Equal(1))
		Expect(digests).To(Equal(1))
		s := st.Snapshot()
		Expect(s.Seen).To(HaveLen(1))
		Expect(s.Seen).To(HaveKey("h/new:t"))
		Expect(s.Generations).To(Equal(map[string]int{"h/new:t": 2}))
		Expect(s.Digests).To(HaveLen(1))
		Expect(s.Digests).To(HaveKey("sha256:new"))
	})

	It("adopts what has no clock instead of dropping it", func() {
		Expect(st.Update(func(s *State) error {
			s.Seen["h/zero:t"] = time.Time{}
			s.Generations["h/orphan:t"] = 2
			s.Digests["sha256:zero"] = DigestEntry{}
			return nil
		})).To(Succeed())
		images, digests, err := st.Expire(now, ttl)
		Expect(err).NotTo(HaveOccurred())
		Expect(images + digests).To(BeZero())
		s := st.Snapshot()
		Expect(s.Seen).To(HaveKeyWithValue("h/zero:t", now))
		Expect(s.Seen).To(HaveKeyWithValue("h/orphan:t", now))
		Expect(s.Generations).To(HaveKeyWithValue("h/orphan:t", 2))
		Expect(s.Digests["sha256:zero"].LastSeen).To(Equal(now))

		// A full ttl later, they go.
		images, digests, err = st.Expire(now.Add(ttl+expireHour), ttl)
		Expect(err).NotTo(HaveOccurred())
		Expect(images).To(Equal(2))
		Expect(digests).To(Equal(1))
		Expect(st.Snapshot().Generations).To(BeEmpty())
	})

	It("writes nothing when nothing expired", func() {
		Expect(st.Update(func(s *State) error {
			s.Seen["h/new:t"] = now
			return nil
		})).To(Succeed())
		before := st.Snapshot()
		images, digests, err := st.Expire(now, ttl)
		Expect(err).NotTo(HaveOccurred())
		Expect(images + digests).To(BeZero())
		Expect(st.Snapshot()).To(BeIdenticalTo(before))
	})
})

var _ = Describe("last-seen refresh", func() {
	var (
		st  *Store
		idx v1.ImageIndex
		d   string
	)
	age := func(by time.Duration) {
		GinkgoHelper()
		Expect(st.Update(func(s *State) error {
			for k, t := range s.Seen {
				s.Seen[k] = t.Add(-by)
			}
			for k, e := range s.Digests {
				e.LastSeen = e.LastSeen.Add(-by)
				s.Digests[k] = e
			}
			return nil
		})).To(Succeed())
	}
	recent := func() OmegaMatcher { return BeTemporally("~", time.Now(), time.Minute) }

	BeforeEach(func() {
		var err error
		st, err = Open(filepath.Join(GinkgoT().TempDir(), "state.json"))
		Expect(err).NotTo(HaveOccurred())
		st.SetRefresh(time.Hour)
		idx, err = synth.Build(expireKey, 1, nil)
		Expect(err).NotTo(HaveOccurred())
		h, err := idx.Digest()
		Expect(err).NotTo(HaveOccurred())
		d = h.String()
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
	})

	It("stamps the seen time and the digests when recording", func() {
		s := st.Snapshot()
		Expect(s.Seen[expireRef]).To(recent())
		Expect(s.Digests).To(HaveLen(5))
		for k, e := range s.Digests {
			Expect(e.LastSeen).To(recent(), k)
		}
	})

	It("does not write again within the refresh interval", func() {
		before := st.Snapshot()
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
		st.TouchDigest(d)
		Expect(st.Snapshot()).To(BeIdenticalTo(before))
	})

	It("refreshes a stale image once, with its digests", func() {
		age(2 * time.Hour)
		old := st.Snapshot()
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
		s := st.Snapshot()
		Expect(s).NotTo(BeIdenticalTo(old))
		Expect(s.Seen[expireRef]).To(recent())
		for k, e := range s.Digests {
			Expect(e.LastSeen).To(recent(), k)
		}
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
		Expect(st.Snapshot()).To(BeIdenticalTo(s))
	})

	It("refreshes when only one digest of the image is stale", func() {
		Expect(st.Update(func(s *State) error {
			e := s.Digests[d]
			e.LastSeen = time.Now().Add(-2 * time.Hour)
			s.Digests[d] = e
			return nil
		})).To(Succeed())
		old := st.Snapshot()
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
		Expect(st.Snapshot()).NotTo(BeIdenticalTo(old))
		Expect(st.Snapshot().Digests[d].LastSeen).To(recent())
	})

	It("never rewrites a seen image when the refresh is off", func() {
		st.SetRefresh(0)
		age(48 * time.Hour)
		before := st.Snapshot()
		Expect(st.RecordImage("upstream", expireKey, "t1", expireKey, 1, idx)).To(Succeed())
		st.TouchDigest(d)
		Expect(st.Snapshot()).To(BeIdenticalTo(before))
	})

	Describe("TouchDigest", func() {
		It("refreshes only the stale digest it names", func() {
			age(2 * time.Hour)
			st.TouchDigest(d)
			s := st.Snapshot()
			Expect(s.Digests[d].LastSeen).To(recent())
			stale := 0
			for k, e := range s.Digests {
				if k != d && e.LastSeen.Before(time.Now().Add(-time.Hour)) {
					stale++
				}
			}
			Expect(stale).To(Equal(4))
		})

		It("ignores an unknown digest", func() {
			before := st.Snapshot()
			st.TouchDigest("sha256:unknown")
			Expect(st.Snapshot()).To(BeIdenticalTo(before))
		})

		It("keeps the refresh in memory when the file cannot be written", func() {
			age(2 * time.Hour)
			p := filepath.Join(GinkgoT().TempDir(), "state.json")
			Expect(os.Mkdir(p+".tmp", 0o700)).To(Succeed())
			st.path = p
			st.TouchDigest(d)
			Expect(st.Snapshot().Digests[d].LastSeen).To(recent())
		})
	})
})
