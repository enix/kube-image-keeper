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
	clusterSkew       *prometheus.GaugeVec

	mu sync.Mutex
	// failing are the label values of the kuik_image_origin_unavailable series of each
	// monitor, so that an image answering again deletes its series.
	failing map[string]map[[3]string]bool
	// skewed are the images each monitor has a kuik_image_cluster_skew_digests series for.
	skewed map[string]map[string]bool
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
		}, []string{seriesKind, seriesName, seriesImage, seriesRegistry, seriesReason}),
		clusterSkew: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_image_cluster_skew_digests",
			Help: "Number of distinct digests running for one image reference. Present only while pods disagree, so its value is always 2 or more",
		}, []string{seriesKind, seriesName, seriesImage, seriesRegistry}),
		failing: map[string]map[[3]string]bool{},
		skewed:  map[string]map[string]bool{},
	}
	for _, c := range []prometheus.Collector{m.originUnavailable, m.clusterSkew} {
		if err := registerer.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// report exports the origin counts of the monitor name, one kuik_image_origin_unavailable
// series per failing image, one kuik_image_drifted series per drifted tag and one
// kuik_image_cluster_skew_digests series per tag pods run at several digests, deleting the
// series of the anomalies that ended: deleting rather than zeroing resolves an alert at once.
func (m *monitorMetrics) report(name string, counts imagemetrics.Counts, failing map[string]kuikv1alpha1.CheckFailureReason, drifted []string, skew map[string]int) {
	m.images.SetCounts(kindImageMonitor, name, imagemetrics.ReferenceOrigin, counts)
	m.images.SetDrifted(kindImageMonitor, name, drifted)

	m.mu.Lock()
	defer m.mu.Unlock()
	skewed := map[string]bool{}
	for image, digests := range skew {
		skewed[image] = true
		m.clusterSkew.WithLabelValues(kindImageMonitor, name, image, registryOf(image)).Set(float64(digests))
	}
	for image := range m.skewed[name] {
		if !skewed[image] {
			m.clusterSkew.DeleteLabelValues(kindImageMonitor, name, image, registryOf(image))
		}
	}
	m.skewed[name] = skewed

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
	m.clusterSkew.DeletePartialMatch(prometheus.Labels{seriesKind: kindImageMonitor, seriesName: name})
	m.mu.Lock()
	delete(m.failing, name)
	delete(m.skewed, name)
	m.mu.Unlock()
}
