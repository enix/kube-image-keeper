package kuik

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/imagepath"
	"github.com/enix/kube-image-keeper/internal/status/imagemetrics"
)

// monitorMetrics are the series an ImageMonitor exports. The HELP texts are those of
// docs/v3/observability.md.
type monitorMetrics struct {
	images            *imagemetrics.Exporter
	originUnavailable *prometheus.GaugeVec

	mu sync.Mutex
	// failing are the label values of the kuik_image_origin_unavailable series of each
	// monitor, so that an image answering again deletes its series.
	failing map[string]map[[3]string]bool
}

func newMonitorMetrics(registerer prometheus.Registerer) (*monitorMetrics, error) {
	images, err := imagemetrics.New(registerer)
	if err != nil {
		return nil, err
	}
	m := &monitorMetrics{
		images: images,
		originUnavailable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_image_origin_unavailable",
			Help: "1 while a tracked origin reference is failing its availability check",
		}, []string{seriesKind, seriesName, "image", "registry", seriesReason}),
		failing: map[string]map[[3]string]bool{},
	}
	if err := registerer.Register(m.originUnavailable); err != nil {
		return nil, err
	}
	return m, nil
}

// report exports the origin counts of the monitor name, one kuik_image_origin_unavailable
// series per failing image and one kuik_image_drifted series per drifted tag, deleting the
// series of the images that answer again or caught up: deleting rather than zeroing resolves
// an alert at once.
func (m *monitorMetrics) report(name string, counts imagemetrics.Counts, failing map[string]kuikv1alpha1.CheckFailureReason, drifted []string) {
	m.images.SetCounts(kindImageMonitor, name, imagemetrics.ReferenceOrigin, counts)
	m.images.SetDrifted(kindImageMonitor, name, drifted)

	m.mu.Lock()
	defer m.mu.Unlock()
	current := map[[3]string]bool{}
	for image, reason := range failing {
		labels := [3]string{image, registryOf(image), string(reason)}
		current[labels] = true
		m.originUnavailable.WithLabelValues(kindImageMonitor, name, labels[0], labels[1], labels[2]).Set(1)
	}
	for labels := range m.failing[name] {
		if !current[labels] {
			m.originUnavailable.DeleteLabelValues(kindImageMonitor, name, labels[0], labels[1], labels[2])
		}
	}
	m.failing[name] = current
}

// registryOf is the registry host of image, as its ring names it: docker.io for a short name.
func registryOf(image string) string {
	ref, err := imagepath.Parse(image)
	if err != nil {
		host, _, _ := strings.Cut(image, "/")
		return host
	}
	return ref.Host
}

// forget deletes every series of the monitor name.
func (m *monitorMetrics) forget(name string) {
	m.images.Forget(kindImageMonitor, name)
	m.originUnavailable.DeletePartialMatch(prometheus.Labels{seriesKind: kindImageMonitor, seriesName: name})
	m.mu.Lock()
	delete(m.failing, name)
	m.mu.Unlock()
}
