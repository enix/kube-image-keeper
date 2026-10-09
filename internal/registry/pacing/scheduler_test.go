package pacing

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/config"
)

const (
	dockerHub = "docker.io"
	quayIO    = "quay.io"
	// destinationHost is the host of a mirror destination, which kuik scans but never paces.
	destinationHost = "registry.tld"
	quayImage       = "quay.io/a/a:1"
	imageA          = "docker.io/library/a:1"
	imageB          = "docker.io/library/b:1"
	imageC          = "docker.io/library/c:1"
	imageX          = "docker.io/library/x:1"
	imageY          = "docker.io/library/y:1"
)

var (
	monitor = Owner{Kind: "ImageMonitor", Name: "cluster-images"}
	mirror  = Owner{Kind: "ImageMirror", Name: "upstream"}
)

var _ = Describe("Scheduler", func() {
	Context("windows", func() {
		It("opens the first window of a host a full interval after the scheduler starts", func() {
			h := run(pacing(time.Minute, time.Hour, nil))
			c := newChecker(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)

			h.advance(59 * time.Second)
			Expect(c.visits()).To(BeEmpty())

			h.advance(time.Second)
			Expect(c.visits()).To(Equal([]visit{{Ref: imageA, At: at(time.Minute)}}))
		})

		It("opens the following windows at start plus a multiple of the interval, whatever each image took", func() {
			h := run(pacing(time.Minute, time.Hour, nil))
			c := newChecker(h)
			c.hold(imageA)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)

			h.step(time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			h.step(20 * time.Second)
			c.release(imageA)
			h.advance(40 * time.Second)

			Expect(c.visits()).To(Equal([]visit{
				{Ref: imageA, At: at(time.Minute)},
				{Ref: imageB, At: at(2 * time.Minute)},
			}))
		})

		It("loses a window that finds nothing to do instead of banking it", func() {
			h := run(pacing(time.Minute, time.Hour, nil))
			c := newChecker(h)
			h.s.SetRing(monitor, dockerHub, nil, kuikv1alpha1.RegistryCheck{}, c)
			for range 3 {
				h.advance(time.Minute)
			}

			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			h.advance(time.Minute)

			Expect(c.visits()).To(Equal([]visit{{Ref: imageA, At: at(4 * time.Minute)}}))
		})

		It("loses a window that opens while the previous check of the host is still running", func() {
			h := run(pacing(time.Minute, time.Hour, nil))
			c := newChecker(h)
			c.hold(imageA)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)

			h.step(time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			h.step(time.Minute)
			h.step(10 * time.Second)
			c.release(imageA)
			h.advance(50 * time.Second)

			Expect(c.visits()).To(Equal([]visit{
				{Ref: imageA, At: at(time.Minute)},
				{Ref: imageB, At: at(3 * time.Minute)},
			}))
		})

		It("loses a window that opens while the previous copy of the host is still transferring", func() {
			h := run(pacing(time.Hour, 3*time.Minute, nil))
			c := newCopier(h)
			c.hold(imageA)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB}, c)

			h.step(3 * time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			h.step(3 * time.Minute)
			h.step(time.Minute)
			c.release(imageA)
			h.advance(2 * time.Minute)

			Expect(c.visits()).To(Equal([]visit{
				{Ref: imageA, At: at(3 * time.Minute)},
				{Ref: imageB, At: at(9 * time.Minute)},
			}))
		})

		It("paces each host on its own series of windows", func() {
			h := run(pacing(time.Minute, time.Hour, map[string]config.RegistryPacing{
				quayIO: {Check: window(3*time.Minute, 0)},
			}))
			docker, quay := newChecker(h), newChecker(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, docker)
			h.s.SetRing(monitor, quayIO, []string{quayImage, "quay.io/b/b:1"}, kuikv1alpha1.RegistryCheck{}, quay)

			for range 3 {
				h.advance(time.Minute)
			}

			Expect(docker.visits()).To(HaveLen(3))
			Expect(quay.visits()).To(Equal([]visit{{Ref: quayImage, At: at(3 * time.Minute)}}))
		})

		It("paces a host without a block of its own on registries.default", func() {
			h := run(pacing(2*time.Minute, time.Hour, map[string]config.RegistryPacing{
				quayIO: {Check: window(5*time.Minute, 0)},
			}))
			c := newChecker(h)
			h.s.SetRing(monitor, "ghcr.io", []string{"ghcr.io/a/a:1", "ghcr.io/b/b:1"}, kuikv1alpha1.RegistryCheck{}, c)

			for range 4 {
				h.advance(time.Minute)
			}

			Expect(c.visits()).To(Equal([]visit{
				{Ref: "ghcr.io/a/a:1", At: at(2 * time.Minute)},
				{Ref: "ghcr.io/b/b:1", At: at(4 * time.Minute)},
			}))
		})

		It("paces the checks and the copies of a host on separate series", func() {
			h := run(pacing(time.Minute, 3*time.Minute, nil))
			checks, copies := newChecker(h), newCopier(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, checks)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB}, copies)

			for range 3 {
				h.advance(time.Minute)
			}

			Expect(checks.visits()).To(HaveLen(3))
			Expect(copies.visits()).To(Equal([]visit{{Ref: imageA, At: at(3 * time.Minute)}}))
		})

		It("abandons a check that outlasts the check timeout of its host", func() {
			h := run(pacing(time.Minute, time.Hour, map[string]config.RegistryPacing{
				dockerHub: {Check: window(0, 10*time.Second)},
			}))
			c := newChecker(h)
			c.hold(imageA)
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)

			h.step(time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			h.step(10 * time.Second)

			Eventually(func() time.Time { return c.abandonedAt(imageA) }).Should(Equal(at(70 * time.Second)))
			Eventually(func() []Response { return c.responses(imageA) }).Should(ConsistOf(
				HaveField("Err", HaveOccurred()),
			))
		})
		It("abandons a copy that outlasts the copy timeout of its host", func() {
			h := run(pacing(time.Hour, 3*time.Minute, map[string]config.RegistryPacing{
				dockerHub: {Copy: window(0, 10*time.Second)},
			}))
			c := newCopier(h)
			c.hold(imageA)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA}, c)

			h.step(3 * time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			h.step(10 * time.Second)

			Eventually(func() time.Time { return c.abandonedAt(imageA) }).Should(Equal(at(3*time.Minute + 10*time.Second)))
		})

		It("lets a copy run as long as it takes when the copy timeout of its host is 0", func() {
			h := run(pacing(time.Hour, 3*time.Minute, map[string]config.RegistryPacing{
				dockerHub: {Copy: &config.Window{Timeout: &config.Duration{}}},
			}))
			c := newCopier(h)
			c.hold(imageA)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA}, c)

			h.step(3 * time.Minute)
			Eventually(c.visits).Should(HaveLen(1))
			for range 24 {
				h.step(time.Hour)
			}

			Consistently(func() time.Time { return c.abandonedAt(imageA) }).Should(BeZero())
			c.release(imageA)
		})
	})

	Context("check rings", func() {
		var (
			h *harness
			c *fakeChecker
		)
		BeforeEach(func() {
			h = run(pacing(time.Minute, time.Hour, nil))
			c = newChecker(h)
		})
		windows := func(n int) {
			GinkgoHelper()
			for range n {
				h.advance(time.Minute)
			}
		}

		It("takes the images of a ring in lexicographic order", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageC, imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			windows(3)
			Expect(c.refs()).To(Equal([]string{imageA, imageB, imageC}))
		})

		It("wraps to the first image after the last one", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			windows(3)
			Expect(c.refs()).To(Equal([]string{imageA, imageB, imageA}))
		})

		It("starts at the first image when no cursor is persisted", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageB, imageA}, kuikv1alpha1.RegistryCheck{Registry: dockerHub, Images: 2}, c)
			windows(1)
			Expect(c.refs()).To(Equal([]string{imageA}))
		})

		It("resumes at the successor of the persisted cursor", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC},
				kuikv1alpha1.RegistryCheck{Registry: dockerHub, Images: 3, Cursor: imageB}, c)
			windows(2)
			Expect(c.refs()).To(Equal([]string{imageC, imageA}))
		})

		It("resumes at the successor of the persisted cursor when that reference is gone", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageC},
				kuikv1alpha1.RegistryCheck{Registry: dockerHub, Images: 3, Cursor: imageB}, c)
			windows(1)
			Expect(c.refs()).To(Equal([]string{imageC}))
		})

		It("keeps its position when its images change", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC}, kuikv1alpha1.RegistryCheck{}, c)
			windows(2)

			h.s.SetRing(monitor, dockerHub, []string{imageA, "docker.io/library/aa:1", imageB, imageC},
				kuikv1alpha1.RegistryCheck{}, c)
			windows(1)

			Expect(c.refs()).To(Equal([]string{imageA, imageB, imageC}))
		})

		It("shares the check windows of a host between the rings of an ImageMonitor and an ImageMirror in round-robin", func() {
			drift := newChecker(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetRing(mirror, dockerHub, []string{imageX, imageY, "docker.io/library/z:1"}, kuikv1alpha1.RegistryCheck{}, drift)

			for range 6 {
				h.advance(time.Minute)
				Expect(len(c.visits()) - len(drift.visits())).To(BeNumerically("~", 0, 1))
			}
			Expect(c.visits()).To(HaveLen(3))
			Expect(drift.visits()).To(HaveLen(3))
		})

		It("never loses a window to a ring with no image", func() {
			h.s.SetRing(Owner{Kind: "ImageMonitor", Name: "empty"}, dockerHub, nil, kuikv1alpha1.RegistryCheck{}, newChecker(h))
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			windows(2)
			Expect(c.visits()).To(HaveLen(2))
		})

		It("stops checking the images of a removed ring", func() {
			gone := newChecker(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetRing(mirror, dockerHub, []string{imageX, imageY}, kuikv1alpha1.RegistryCheck{}, gone)

			h.s.RemoveRing(mirror, dockerHub)
			windows(2)

			Expect(gone.visits()).To(BeEmpty())
			Expect(c.visits()).To(HaveLen(2))
			Expect(h.s.RegistryChecks(mirror)).To(BeEmpty())
		})
	})

	Context("verdict cache", func() {
		var (
			h               *harness
			monitors, drift *fakeChecker
		)
		BeforeEach(func() {
			h = run(pacing(time.Minute, time.Hour, nil))
			monitors, drift = newChecker(h), newChecker(h)
		})
		windows := func(n int) {
			GinkgoHelper()
			for range n {
				h.advance(time.Minute)
			}
		}
		lap := func(owner Owner) *metav1.Duration {
			GinkgoHelper()
			checks := h.s.RegistryChecks(owner)
			Expect(checks).To(HaveLen(1))
			return checks[0].CycleDuration
		}
		images := []string{imageA, imageB, imageC, "docker.io/library/d:1"}

		It("reuses a response another ring obtained after this ring last visited the image, without spending a window", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, monitors)
			h.s.SetRing(mirror, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, drift)
			windows(2)

			Expect(drift.refs()).NotTo(ContainElement(imageA))
			Expect(drift.responses(imageA)).To(Equal([]Response{answer(imageA)}))
			Expect(len(monitors.visits()) + len(drift.visits())).To(Equal(2))
		})

		It("spends a window on an image whose cached response is older than this ring's last visit of it", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC}, kuikv1alpha1.RegistryCheck{}, monitors)
			h.s.SetRing(mirror, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, drift)
			windows(2)
			Expect(drift.visits()).To(BeEmpty())

			windows(1)
			Expect(drift.visits()).To(Equal([]visit{{Ref: imageA, At: at(3 * time.Minute)}}))
		})

		It("laps two rings created together over the same images in the windows of one", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, monitors)
			h.s.SetRing(mirror, dockerHub, images, kuikv1alpha1.RegistryCheck{}, drift)
			windows(10)

			Expect(lap(monitor)).To(PointTo(HaveField("Duration", 4*time.Minute)))
			Expect(lap(mirror)).To(PointTo(HaveField("Duration", 4*time.Minute)))
		})

		It("brings a ring created in the middle of another's lap over the same images down to the windows of one", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, monitors)
			windows(2)
			h.s.SetRing(mirror, dockerHub, images, kuikv1alpha1.RegistryCheck{}, drift)
			windows(16)

			Expect(lap(monitor)).To(PointTo(HaveField("Duration", 4*time.Minute)))
			Expect(lap(mirror)).To(PointTo(HaveField("Duration", 4*time.Minute)))
		})
	})

	Context("laps", func() {
		var (
			h *harness
			c *fakeChecker
		)
		BeforeEach(func() {
			h = run(pacing(time.Minute, time.Hour, nil))
			c = newChecker(h)
		})
		windows := func(n int) {
			GinkgoHelper()
			for range n {
				h.advance(time.Minute)
			}
		}
		ring := func() kuikv1alpha1.RegistryCheck {
			GinkgoHelper()
			checks := h.s.RegistryChecks(monitor)
			Expect(checks).To(HaveLen(1))
			return checks[0]
		}
		images := []string{imageA, imageB}

		It("reports the size of the ring and the last reference checked as its cursor", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC}, kuikv1alpha1.RegistryCheck{}, c)
			windows(2)
			Expect(ring()).To(And(
				HaveField("Registry", dockerHub),
				HaveField("Images", int32(3)),
				HaveField("Cursor", imageB),
			))
		})

		It("starts a lap when a new ring takes its first image", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, c)
			h.settle()
			Expect(ring().CycleStarted).To(BeNil())

			windows(1)
			Expect(ring().CycleStarted).To(PointTo(HaveField("Time", BeTemporally("==", at(time.Minute)))))
		})

		It("reports no cycleDuration until a first lap completes", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, c)
			windows(2)
			Expect(ring().CycleDuration).To(BeNil())
		})

		It("measures cycleDuration from cycleStarted to the ring wrapping to its first image", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, c)
			windows(3)
			Expect(ring().CycleDuration).To(PointTo(HaveField("Duration", 2*time.Minute)))
		})

		It("starts the next lap when the ring wraps", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{}, c)
			windows(3)
			Expect(ring().CycleStarted).To(PointTo(HaveField("Time", BeTemporally("==", at(3*time.Minute)))))
		})

		It("keeps the persisted cycleStarted across a restart, so the lap counts the downtime", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{
				Registry:     dockerHub,
				Images:       2,
				Cursor:       imageA,
				CycleStarted: &metav1.Time{Time: at(-30 * time.Minute)},
			}, c)

			windows(1)
			Expect(ring().CycleStarted).To(PointTo(HaveField("Time", BeTemporally("==", at(-30*time.Minute)))))

			windows(1)
			Expect(ring().CycleDuration).To(PointTo(HaveField("Duration", 32*time.Minute)))
		})

		It("starts a lap at the next wrap when the persisted status has a cursor but no cycleStarted", func() {
			h.s.SetRing(monitor, dockerHub, images, kuikv1alpha1.RegistryCheck{
				Registry: dockerHub,
				Images:   2,
				Cursor:   imageA,
			}, c)

			windows(1)
			Expect(ring().CycleStarted).To(BeNil())

			windows(1)
			Expect(ring().CycleStarted).To(PointTo(HaveField("Time", BeTemporally("==", at(2*time.Minute)))))
			Expect(ring().CycleDuration).To(BeNil())

			windows(2)
			Expect(ring().CycleDuration).To(PointTo(HaveField("Duration", 2*time.Minute)))
		})
	})

	Context("copy queues", func() {
		var (
			h *harness
			c *fakeCopier
		)
		BeforeEach(func() {
			h = run(pacing(time.Hour, 3*time.Minute, nil))
			c = newCopier(h)
		})
		windows := func(n int) {
			GinkgoHelper()
			for range n {
				h.advance(3 * time.Minute)
			}
		}

		It("copies one image of a host's queue per copy window", func() {
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB, imageC}, c)
			h.advance(2 * time.Minute)
			Expect(c.visits()).To(BeEmpty())

			h.advance(time.Minute)
			h.advance(3 * time.Minute)
			Expect(c.visits()).To(Equal([]visit{
				{Ref: imageA, At: at(3 * time.Minute)},
				{Ref: imageB, At: at(6 * time.Minute)},
			}))
		})

		It("takes the images of a mirror in the order it owes them", func() {
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageC, imageA, imageB}, c)
			windows(3)
			Expect(c.refs()).To(Equal([]string{imageC, imageA, imageB}))
		})

		It("moves on to the next image a mirror owes after each attempt, failed or not", func() {
			c.fail(imageA)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB, imageC}, c)
			windows(4)
			Expect(c.refs()).To(Equal([]string{imageA, imageB, imageC, imageA}))
		})

		It("keeps its position when the image it just copied leaves the queue", func() {
			c.fail(imageA)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB, imageC}, c)
			windows(2)

			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageC}, c)
			windows(2)

			Expect(c.refs()).To(Equal([]string{imageA, imageB, imageC, imageA}))
		})

		It("shares the copy windows of a source host between mirrors in round-robin", func() {
			other := newCopier(h)
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB, imageC}, c)
			h.s.SetCopyQueue(Owner{Kind: "ImageMirror", Name: "other"}, dockerHub, []string{imageX, imageY}, other)

			for range 4 {
				h.advance(3 * time.Minute)
				Expect(len(c.visits()) - len(other.visits())).To(BeNumerically("~", 0, 1))
			}
			Expect(c.visits()).To(HaveLen(2))
			Expect(other.visits()).To(HaveLen(2))
		})

		It("stops copying an image once it leaves the queue", func() {
			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageB, imageC}, c)
			windows(1)

			h.s.SetCopyQueue(mirror, dockerHub, []string{imageA, imageC}, c)
			windows(1)
			h.s.SetCopyQueue(mirror, dockerHub, nil, c)
			windows(2)

			Expect(c.refs()).To(Equal([]string{imageA, imageC}))
		})
	})

	Context("config reload", func() {
		var (
			h      *harness
			checks *fakeChecker
			copies *fakeCopier
		)
		BeforeEach(func() {
			h = run(pacing(time.Minute, 3*time.Minute, nil))
			checks, copies = newChecker(h), newCopier(h)
		})
		// until moves the clock by steps of 30s up to origin plus d.
		until := func(d time.Duration) {
			GinkgoHelper()
			for h.clk.Now().Before(at(d)) {
				h.advance(30 * time.Second)
			}
		}
		// offsets returns when each visit happened, from origin.
		offsets := func(visits []visit) []time.Duration {
			ds := make([]time.Duration, 0, len(visits))
			for _, v := range visits {
				ds = append(ds, v.At.Sub(origin))
			}
			return ds
		}
		images := []string{imageA, imageB, imageC, imageX, imageY}
		ring := func(host string, c Checker) {
			h.s.SetRing(monitor, host, images, kuikv1alpha1.RegistryCheck{}, c)
		}
		block := func(host string, b config.RegistryPacing) *config.Config {
			return pacing(time.Minute, 3*time.Minute, map[string]config.RegistryPacing{host: b})
		}

		It("restarts the check series of a host whose check interval changed, a full new interval after the reload", func() {
			ring(dockerHub, checks)
			until(90 * time.Second)

			h.s.SetConfig(block(dockerHub, config.RegistryPacing{Check: window(2*time.Minute, 0)}))
			until(4 * time.Minute)

			Expect(offsets(checks.visits())).To(Equal([]time.Duration{time.Minute, 210 * time.Second}))
		})

		It("restarts the copy series of a host whose copy interval changed, a full new interval after the reload", func() {
			h.s.SetCopyQueue(mirror, dockerHub, images, copies)
			until(4 * time.Minute)

			h.s.SetConfig(block(dockerHub, config.RegistryPacing{Copy: window(5*time.Minute, 0)}))
			until(10 * time.Minute)

			Expect(offsets(copies.visits())).To(Equal([]time.Duration{3 * time.Minute, 9 * time.Minute}))
		})

		It("keeps the copy phase of a host whose check interval alone changed", func() {
			ring(dockerHub, checks)
			h.s.SetCopyQueue(mirror, dockerHub, images, copies)
			until(90 * time.Second)

			h.s.SetConfig(block(dockerHub, config.RegistryPacing{Check: window(2*time.Minute, 0)}))
			until(6 * time.Minute)

			Expect(offsets(copies.visits())).To(Equal([]time.Duration{3 * time.Minute, 6 * time.Minute}))
		})

		It("keeps the check phase of a host whose copy interval alone changed", func() {
			ring(dockerHub, checks)
			h.s.SetCopyQueue(mirror, dockerHub, images, copies)
			until(90 * time.Second)

			h.s.SetConfig(block(dockerHub, config.RegistryPacing{Copy: window(5*time.Minute, 0)}))
			until(3 * time.Minute)

			Expect(offsets(checks.visits())).To(Equal([]time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}))
		})

		// visitsOf reads the visits of the series an entry watches.
		type visitsOf func(*fakeChecker, *fakeCopier) []visit
		checkVisits := func(c *fakeChecker, _ *fakeCopier) []visit { return c.visits() }
		copyVisits := func(_ *fakeChecker, c *fakeCopier) []visit { return c.visits() }

		DescribeTable("keeps the phase of a series whose timeout alone changed",
			func(reloaded config.RegistryPacing, end time.Duration, of visitsOf, want []time.Duration) {
				ring(dockerHub, checks)
				h.s.SetCopyQueue(mirror, dockerHub, images, copies)
				until(90 * time.Second)

				h.s.SetConfig(block(dockerHub, reloaded))
				until(end)

				Expect(offsets(of(checks, copies))).To(Equal(want))
			},
			Entry("check.timeout", config.RegistryPacing{Check: window(0, 20*time.Second)}, 3*time.Minute,
				visitsOf(checkVisits), []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}),
			Entry("copy.timeout", config.RegistryPacing{Copy: window(0, 20*time.Second)}, 6*time.Minute,
				visitsOf(copyVisits), []time.Duration{3 * time.Minute, 6 * time.Minute}),
		)

		It("keeps the phase of a host whose settings did not change", func() {
			ring(dockerHub, checks)
			ring(quayIO, newChecker(h))
			until(90 * time.Second)

			h.s.SetConfig(block(quayIO, config.RegistryPacing{Check: window(2*time.Minute, 0)}))
			until(3 * time.Minute)

			Expect(offsets(checks.visits())).To(Equal([]time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}))
		})

		It("re-phases the hosts that inherit a changed interval of registries.default", func() {
			ring("ghcr.io", checks)
			until(90 * time.Second)

			h.s.SetConfig(pacing(2*time.Minute, 3*time.Minute, nil))
			until(4 * time.Minute)

			Expect(offsets(checks.visits())).To(Equal([]time.Duration{time.Minute, 210 * time.Second}))
		})

		It("keeps the phase of a host whose own block overrides the changed default interval", func() {
			quay := config.RegistryPacing{Check: window(time.Minute, 0)}
			h.s.SetConfig(block(quayIO, quay))
			ring(quayIO, checks)
			until(90 * time.Second)

			h.s.SetConfig(pacing(2*time.Minute, 3*time.Minute, map[string]config.RegistryPacing{quayIO: quay}))
			until(3 * time.Minute)

			Expect(offsets(checks.visits())).To(Equal([]time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}))
		})

		It("moves no ring cursor", func() {
			ring(dockerHub, checks)
			until(2 * time.Minute)

			h.s.SetConfig(block(dockerHub, config.RegistryPacing{Check: window(2*time.Minute, 0)}))
			Expect(h.s.RegistryChecks(monitor)).To(ConsistOf(HaveField("Cursor", imageB)))

			until(4 * time.Minute)
			Expect(checks.refs()).To(Equal([]string{imageA, imageB, imageC}))
		})
	})

	Context("metrics", func() {
		const (
			monitorRing = "kind=ImageMonitor,name=cluster-images,registry=docker.io"
			mirrorRing  = "kind=ImageMirror,name=upstream,registry=docker.io"
		)
		var (
			h *harness
			c *fakeChecker
		)
		BeforeEach(func() {
			h = run(pacing(time.Minute, 3*time.Minute, nil))
			c = newChecker(h)
		})
		windows := func(n int) {
			GinkgoHelper()
			for range n {
				h.advance(time.Minute)
			}
		}

		It("exposes the last completed lap of each ring as kuik_check_cycle_duration_seconds by kind, name and registry", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetRing(mirror, dockerHub, []string{imageX}, kuikv1alpha1.RegistryCheck{}, newChecker(h))
			windows(4)

			Expect(gauges(h.s.Collector(), "kuik_check_cycle_duration_seconds")).To(Equal(map[string]float64{
				monitorRing: 120,
				mirrorRing:  120,
			}))
		})

		It("omits kuik_check_cycle_duration_seconds for a ring until its first lap completes", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			windows(2)
			Expect(gauges(h.s.Collector(), "kuik_check_cycle_duration_seconds")).To(BeEmpty())
		})

		It("exposes the start of the lap in progress as kuik_check_cycle_started_timestamp_seconds", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, c)
			windows(1)
			Expect(gauges(h.s.Collector(), "kuik_check_cycle_started_timestamp_seconds")).To(Equal(map[string]float64{
				monitorRing: float64(at(time.Minute).Unix()),
			}))
		})

		It("exposes the size of each ring as kuik_check_ring_images", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB, imageC}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetRing(mirror, dockerHub, []string{imageX, imageY}, kuikv1alpha1.RegistryCheck{}, newChecker(h))
			Expect(gauges(h.s.Collector(), "kuik_check_ring_images")).To(Equal(map[string]float64{
				monitorRing: 3,
				mirrorRing:  2,
			}))
		})

		It("exposes the check and copy intervals of every scheduled host as kuik_registry_interval_seconds", func() {
			h.s.SetConfig(pacing(time.Minute, 3*time.Minute, map[string]config.RegistryPacing{
				quayIO: {Check: window(5*time.Minute, 0)},
			}))
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetCopyQueue(mirror, quayIO, []string{quayImage}, newCopier(h))

			Expect(gauges(h.s.Collector(), "kuik_registry_interval_seconds")).To(Equal(map[string]float64{
				"operation=Check,registry=docker.io": 60,
				"operation=Copy,registry=docker.io":  180,
				"operation=Check,registry=quay.io":   300,
				"operation=Copy,registry=quay.io":    180,
			}))
		})

		It("exposes the destination scan interval of a mirror destination host with operation Scan", func() {
			cfg := pacing(time.Minute, 3*time.Minute, nil)
			cfg.Mirror.DestinationScan.Interval = config.Duration{Duration: time.Hour}
			h.s.SetConfig(cfg)
			h.s.SetDestinationScan(mirror, destinationHost)

			Expect(gauges(h.s.Collector(), "kuik_registry_interval_seconds")).To(HaveKeyWithValue("operation=Scan,registry="+destinationHost, 3600.0))
		})

		It("drops the Scan series of a destination host once its last mirror releases it", func() {
			h.s.SetDestinationScan(mirror, destinationHost)
			h.s.RemoveDestinationScan(mirror)

			Expect(gauges(h.s.Collector(), "kuik_registry_interval_seconds")).NotTo(HaveKey("operation=Scan,registry=" + destinationHost))
		})

		It("keeps the Scan series of a destination host while another mirror still scans it", func() {
			other := Owner{Kind: mirror.Kind, Name: "other"}
			h.s.SetDestinationScan(mirror, destinationHost)
			h.s.SetDestinationScan(other, destinationHost)
			h.s.RemoveDestinationScan(mirror)

			Expect(gauges(h.s.Collector(), "kuik_registry_interval_seconds")).To(HaveKey("operation=Scan,registry=" + destinationHost))
		})

		It("moves the Scan series to the new destination host of a mirror", func() {
			h.s.SetDestinationScan(mirror, destinationHost)
			h.s.SetDestinationScan(mirror, quayIO)

			series := gauges(h.s.Collector(), "kuik_registry_interval_seconds")
			Expect(series).To(HaveKey("operation=Scan,registry=" + quayIO))
			Expect(series).NotTo(HaveKey("operation=Scan,registry=" + destinationHost))
		})

		It("opens no window on a destination host whose scan interval it exposes", func() {
			h.s.SetDestinationScan(mirror, destinationHost)
			windows(3)

			series := gauges(h.s.Collector(), "kuik_registry_interval_seconds")
			Expect(series).To(HaveKey("operation=Scan,registry=" + destinationHost))
			Expect(series).NotTo(HaveKey("operation=Check,registry=" + destinationHost))
			Expect(series).NotTo(HaveKey("operation=Copy,registry=" + destinationHost))
		})

		It("reflects a reload in kuik_registry_interval_seconds", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetConfig(pacing(2*time.Minute, 3*time.Minute, nil))
			Expect(gauges(h.s.Collector(), "kuik_registry_interval_seconds")).To(HaveKeyWithValue("operation=Check,registry=docker.io", 120.0))
		})

		It("drops the series of a removed ring", func() {
			h.s.SetRing(monitor, dockerHub, []string{imageA}, kuikv1alpha1.RegistryCheck{}, c)
			h.s.SetRing(mirror, dockerHub, []string{imageX}, kuikv1alpha1.RegistryCheck{}, newChecker(h))
			windows(4)

			h.s.RemoveRing(mirror, dockerHub)

			for _, name := range []string{
				"kuik_check_cycle_duration_seconds",
				"kuik_check_cycle_started_timestamp_seconds",
				"kuik_check_ring_images",
			} {
				Expect(gauges(h.s.Collector(), name)).NotTo(HaveKey(mirrorRing), name)
				Expect(gauges(h.s.Collector(), name)).To(HaveKey(monitorRing), name)
			}
		})
	})
})
