package kuik

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/config"
)

// An interrupted destination pass sets no lastPass, so passDue must still hold it back for the
// scan interval: the reconciles a copy end or a pod event trigger meanwhile do not run it again.
var _ = Describe("passDue after an interrupted destination pass", func() {
	const interval = time.Minute
	var (
		r     *ImageMirrorReconciler
		st    *mirrorState
		start time.Time
	)

	BeforeEach(func() {
		r = &ImageMirrorReconciler{}
		cfg := config.Config{}
		cfg.Mirror.DestinationScan.Interval = config.Duration{Duration: interval}
		r.config.Store(&cfg)
		st = &mirrorState{}
		start = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	})

	It("runs the first pass of a mirror at once", func() {
		due, _ := r.passDue(st, start)
		Expect(due).To(BeTrue())
	})

	It("holds an interrupted pass back, and says how long is left of the scan interval", func() {
		r.interrupted(st, start)

		due, wait := r.passDue(st, start.Add(5*time.Second))
		Expect(due).To(BeFalse())
		Expect(wait).To(Equal(interval - 5*time.Second))
	})

	It("runs an interrupted pass again once the scan interval has elapsed", func() {
		r.interrupted(st, start)

		due, _ := r.passDue(st, start.Add(interval))
		Expect(due).To(BeTrue())
	})

	It("runs at once the pass a sweep waited for the copies to end for", func() {
		r.interrupted(st, start)
		st.inFlight, st.sweepAgain = 1, true
		st.ended()

		due, _ := r.passDue(st, start.Add(time.Second))
		Expect(due).To(BeTrue())
	})
})
