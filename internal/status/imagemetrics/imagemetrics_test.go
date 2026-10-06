package imagemetrics

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

// app is an origin reference drifting in the specs below.
const app = "quay.io/acme/app:prod"

// series returns the series of metric on registry, keyed by their sorted labels.
func series(registry *prometheus.Registry, metric string) map[string]float64 {
	GinkgoHelper()
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != metric {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := make([]string, 0, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			slices.Sort(labels)
			out[strings.Join(labels, ",")] = m.GetGauge().GetValue()
		}
	}
	return out
}

var _ = Describe("Exporter", func() {
	var (
		registry *prometheus.Registry
		exporter *Exporter
	)
	BeforeEach(func() {
		registry = prometheus.NewRegistry()
		var err error
		exporter, err = New(registry)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("kuik_images_tracked and kuik_images_checked", func() {
		It("exports the tracked references of a resource and population by running, standby and retained", func() {
			exporter.SetCounts("ImageMirror", "prod", ReferenceCopy, Counts{Running: 5, Standby: 2, Retained: 1})

			Expect(series(registry, "kuik_images_tracked")).To(Equal(map[string]float64{
				"kind=ImageMirror,name=prod,reference=copy,state=running":  5,
				"kind=ImageMirror,name=prod,reference=copy,state=standby":  2,
				"kind=ImageMirror,name=prod,reference=copy,state=retained": 1,
			}))
		})

		It("exports the checked references of a resource and population by available and unavailable", func() {
			exporter.SetCounts("ImageMirror", "prod", ReferenceCopy, Counts{Available: 6, Unavailable: 2})

			Expect(series(registry, "kuik_images_checked")).To(Equal(map[string]float64{
				"kind=ImageMirror,name=prod,reference=copy,state=available":   6,
				"kind=ImageMirror,name=prod,reference=copy,state=unavailable": 2,
			}))
		})

		It("keeps the series of one population when another population of the same resource is set", func() {
			exporter.SetCounts("ImageMonitor", "cluster", ReferenceOrigin, Counts{Running: 3})
			exporter.SetCounts("ImageMonitor", "cluster", ReferenceAlternatives, Counts{Running: 1})

			Expect(series(registry, "kuik_images_tracked")).To(And(
				HaveKeyWithValue("kind=ImageMonitor,name=cluster,reference=origin,state=running", 3.0),
				HaveKeyWithValue("kind=ImageMonitor,name=cluster,reference=alternatives,state=running", 1.0),
			))
		})
	})

	Context("kuik_image_drifted", func() {
		It("exports one series per drifted image, its registry label the host of the image", func() {
			exporter.SetDrifted("ImageMirror", "prod", []string{app, "docker.io/library/nginx:latest"})

			Expect(series(registry, "kuik_image_drifted")).To(Equal(map[string]float64{
				"image=quay.io/acme/app:prod,kind=ImageMirror,name=prod,registry=quay.io":            1,
				"image=docker.io/library/nginx:latest,kind=ImageMirror,name=prod,registry=docker.io": 1,
			}))
		})

		It("deletes the series of an image that no longer drifts", func() {
			exporter.SetDrifted("ImageMirror", "prod", []string{app, "quay.io/acme/tool:1"})
			exporter.SetDrifted("ImageMirror", "prod", []string{"quay.io/acme/tool:1"})

			Expect(series(registry, "kuik_image_drifted")).To(Equal(map[string]float64{
				"image=quay.io/acme/tool:1,kind=ImageMirror,name=prod,registry=quay.io": 1,
			}))
		})
	})

	It("reuses the collectors another controller registered on the same registry", func() {
		other, err := New(registry)
		Expect(err).NotTo(HaveOccurred())

		exporter.SetCounts("ImageMirror", "prod", ReferenceCopy, Counts{Running: 1})
		other.SetCounts("ImageMonitor", "cluster", ReferenceOrigin, Counts{Running: 4})

		Expect(series(registry, "kuik_images_tracked")).To(And(
			HaveKeyWithValue("kind=ImageMirror,name=prod,reference=copy,state=running", 1.0),
			HaveKeyWithValue("kind=ImageMonitor,name=cluster,reference=origin,state=running", 4.0),
		))
	})

	It("removes every series of a forgotten resource", func() {
		exporter.SetCounts("ImageMirror", "prod", ReferenceCopy, Counts{Running: 1, Available: 1})
		exporter.SetDrifted("ImageMirror", "prod", []string{app})
		exporter.SetCounts("ImageMirror", "staging", ReferenceCopy, Counts{Running: 2})

		exporter.Forget("ImageMirror", "prod")

		Expect(series(registry, "kuik_images_tracked")).NotTo(HaveKey(ContainSubstring("name=prod,")))
		Expect(series(registry, "kuik_images_checked")).NotTo(HaveKey(ContainSubstring("name=prod,")))
		Expect(series(registry, "kuik_image_drifted")).To(BeEmpty())
		Expect(series(registry, "kuik_images_tracked")).To(HaveKey(ContainSubstring("name=staging,")))
	})
})
