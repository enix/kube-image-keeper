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
	imageA    = "docker.io/library/a:1"
	imageB    = "docker.io/library/b:1"
	imageC    = "docker.io/library/c:1"
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

		PIt("loses a window that opens while the previous copy of the host is still transferring", func() {})

		It("paces each host on its own series of windows", func() {
			h := run(pacing(time.Minute, time.Hour, map[string]config.RegistryPacing{
				"quay.io": {Check: window(3*time.Minute, 0)},
			}))
			docker, quay := newChecker(h), newChecker(h)
			h.s.SetRing(monitor, dockerHub, []string{imageA, imageB}, kuikv1alpha1.RegistryCheck{}, docker)
			h.s.SetRing(monitor, "quay.io", []string{"quay.io/a/a:1", "quay.io/b/b:1"}, kuikv1alpha1.RegistryCheck{}, quay)

			for range 3 {
				h.advance(time.Minute)
			}

			Expect(docker.visits()).To(HaveLen(3))
			Expect(quay.visits()).To(Equal([]visit{{Ref: "quay.io/a/a:1", At: at(3 * time.Minute)}}))
		})

		It("paces a host without a block of its own on registries.default", func() {
			h := run(pacing(2*time.Minute, time.Hour, map[string]config.RegistryPacing{
				"quay.io": {Check: window(5*time.Minute, 0)},
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

		PIt("paces the checks and the copies of a host on separate series", func() {})

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
		PIt("abandons a copy that outlasts the copy timeout of its host", func() {})
		PIt("lets a copy run as long as it takes when the copy timeout of its host is 0", func() {})
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
			h.s.SetRing(mirror, dockerHub, []string{"docker.io/library/x:1", "docker.io/library/y:1", "docker.io/library/z:1"}, kuikv1alpha1.RegistryCheck{}, drift)

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
			h.s.SetRing(mirror, dockerHub, []string{"docker.io/library/x:1", "docker.io/library/y:1"}, kuikv1alpha1.RegistryCheck{}, gone)

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
		PIt("copies one image of a host's queue per copy window", func() {})
		PIt("takes the images of a mirror in the order it owes them", func() {})
		PIt("moves on to the next image a mirror owes after each attempt, failed or not", func() {})
		PIt("shares the copy windows of a source host between mirrors in round-robin", func() {})
		PIt("stops copying an image once it leaves the queue", func() {})
	})

	Context("config reload", func() {
		PIt("restarts the check series of a host whose check interval changed, a full new interval after the reload", func() {})
		PIt("restarts the copy series of a host whose copy interval changed, a full new interval after the reload", func() {})
		PIt("keeps the copy phase of a host whose check interval alone changed", func() {})
		PIt("keeps the check phase of a host whose copy interval alone changed", func() {})
		DescribeTable("keeps the phase of a series whose timeout alone changed",
			func(field string) {},
			PEntry("check.timeout", "check.timeout"),
			PEntry("copy.timeout", "copy.timeout"),
		)
		PIt("keeps the phase of a host whose settings did not change", func() {})
		PIt("re-phases the hosts that inherit a changed interval of registries.default", func() {})
		PIt("keeps the phase of a host whose own block overrides the changed default interval", func() {})
		PIt("moves no ring cursor", func() {})
	})

	Context("metrics", func() {
		PIt("exposes the last completed lap of each ring as kuik_check_cycle_duration_seconds by kind, name and registry", func() {})
		PIt("omits kuik_check_cycle_duration_seconds for a ring until its first lap completes", func() {})
		PIt("exposes the start of the lap in progress as kuik_check_cycle_started_timestamp_seconds", func() {})
		PIt("exposes the size of each ring as kuik_check_ring_images", func() {})
		PIt("exposes the check and copy intervals of every scheduled host as kuik_registry_interval_seconds", func() {})
		PIt("reflects a reload in kuik_registry_interval_seconds", func() {})
		PIt("drops the series of a removed ring", func() {})
	})
})
