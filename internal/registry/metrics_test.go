package registry

import (
	"github.com/prometheus/client_golang/prometheus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

var _ = Describe("RegisterMetrics", func() {
	It("serves kuik_registry_requests_total on the registerer, however many readers of the process register it", func() {
		metrics := prometheus.NewRegistry()
		Expect(RegisterMetrics(metrics)).To(Succeed())
		Expect(RegisterMetrics(metrics)).To(Succeed())
		reg := newRegistry()
		reg.Push("app:v1", registrytest.Image())

		_, err := NewClient().Check(ctx, endpoint(reg, "app:v1"))
		Expect(err).NotTo(HaveOccurred())

		families, err := metrics.Gather()
		Expect(err).NotTo(HaveOccurred())
		var served float64
		for _, family := range families {
			if family.GetName() == "kuik_registry_requests_total" {
				for _, m := range family.GetMetric() {
					served += m.GetCounter().GetValue()
				}
			}
		}
		Expect(served).To(Equal(1.0))
	})
})
