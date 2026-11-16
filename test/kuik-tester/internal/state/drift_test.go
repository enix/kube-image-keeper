package state

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const (
	driftHost   = "upstream"
	driftAlt    = "alt1"
	driftRepo   = "acme/foo"
	driftRef    = "upstream/acme/foo:t1"
	driftAltRef = "alt1/acme/foo:t1"
)

var _ = Describe("Generation", func() {
	DescribeTable("is the stored generation, 1 by default",
		func(stored map[string]int, want int) {
			st := &State{Generations: stored}
			Expect(st.Generation(driftHost, driftRepo, "t1")).To(Equal(want))
		},
		Entry("no entry", map[string]int{}, 1),
		Entry("stored 3", map[string]int{driftRef: 3}, 3),
		Entry("stored 0", map[string]int{driftRef: 0}, 1),
	)
})

var _ = Describe("Drift", func() {
	It("bumps only the generations of the refs its target covers", func() {
		s := &State{
			Generations: map[string]int{},
			Seen: map[string]time.Time{
				driftRef:    time.Now(),
				driftAltRef: time.Now(),
			},
		}
		got := s.Drift(api.Target{Host: driftHost, Repo: driftRepo, Tag: "t1"})
		Expect(got).To(Equal([]string{driftRef}))
		Expect(s.Generation(driftHost, driftRepo, "t1")).To(Equal(2), "upstream gen")
		Expect(s.Generation(driftAlt, driftRepo, "t1")).To(Equal(1), "alt1 gen")
		got = s.Drift(api.Target{Host: "*"})
		Expect(got).To(Equal([]string{driftAltRef, driftRef}))
		Expect(s.Generation(driftHost, driftRepo, "t1")).To(Equal(3), "gens %v", s.Generations)
		Expect(s.Generation(driftAlt, driftRepo, "t1")).To(Equal(2), "gens %v", s.Generations)
	})

	It("marks the refs it changes as seen now, and only those", func() {
		old := time.Now().Add(-48 * time.Hour)
		s := &State{
			Generations: map[string]int{},
			Seen:        map[string]time.Time{driftRef: old, driftAltRef: old},
		}
		s.Drift(api.Target{Host: driftHost, Repo: driftRepo, Tag: "t1"})
		Expect(s.Seen[driftRef]).To(BeTemporally("~", time.Now(), time.Minute))
		Expect(s.Seen[driftAltRef]).To(Equal(old))
	})

	It("marks a drifted ref never seen as seen now", func() {
		s := &State{Generations: map[string]int{}, Seen: map[string]time.Time{}}
		s.Drift(api.Target{Host: driftHost, Repo: driftRepo, Tag: "t9"})
		Expect(s.Seen["upstream/acme/foo:t9"]).To(BeTemporally("~", time.Now(), time.Minute))
	})

	It("drifts a literal ref never seen but not a glob that matches nothing", func() {
		s := &State{Generations: map[string]int{}, Seen: map[string]time.Time{}}
		got := s.Drift(api.Target{Host: driftHost, Repo: driftRepo, Tag: "t9"})
		Expect(got).To(Equal([]string{"upstream/acme/foo:t9"}))
		Expect(s.Generation(driftHost, driftRepo, "t9")).To(Equal(2), "gens %v", s.Generations)
		s = &State{Generations: map[string]int{}, Seen: map[string]time.Time{}}
		got = s.Drift(api.Target{Host: driftHost, Repo: "acme/*", Tag: "t9"})
		Expect(got).To(BeEmpty())
		Expect(s.Generations).To(BeEmpty())
	})
})
