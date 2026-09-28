package info

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

var _ = Describe("NewCollector", func() {
	It("exposes kuik_build_info at 1, labelled with the version set at link time", func() {
		DeferCleanup(func(version string) { Version = version }, Version)
		Version = "1.2.3"

		registry := prometheus.NewPedanticRegistry()
		Expect(registry.Register(NewCollector())).To(Succeed())
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())

		Expect(families).To(HaveLen(1))
		Expect(families[0].GetName()).To(Equal("kuik_build_info"))
		metric := families[0].GetMetric()
		Expect(metric).To(HaveLen(1))
		Expect(metric[0].GetGauge().GetValue()).To(Equal(1.0))
		labels := map[string]string{}
		for _, label := range metric[0].GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		Expect(labels).To(HaveKeyWithValue("version", "1.2.3"))
	})
})
