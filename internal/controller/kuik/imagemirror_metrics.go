package kuik

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/status/imagemetrics"
)

// The labels the mirror series share.
const (
	seriesKind   = "kind"
	seriesName   = "name"
	seriesReason = "reason"
)

// The reasons of kuik_mirror_copies_total and kuik_mirror_tags_deleted_total.
const (
	copyInitial   = "Initial"
	copyRecopy    = "Recopy"
	copyResync    = "Resync"
	deletedUnused = "Unused"
	deletedOrphan = "Orphan"
)

// mirrorMetrics are the series an ImageMirror exports beyond its routing side. The HELP texts
// are those of docs/v3/observability.md.
type mirrorMetrics struct {
	images      *imagemetrics.Exporter
	orphanTags  *prometheus.GaugeVec
	copyFailed  *prometheus.GaugeVec
	copies      *prometheus.CounterVec
	deleted     *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	selfChecked *prometheus.GaugeVec

	mu sync.Mutex
	// failing are the label values of the kuik_image_copy_failed series of each mirror, so
	// that a copy that succeeds again deletes its series.
	failing map[string]map[[3]string]bool
}

func newMirrorMetrics(registerer prometheus.Registerer) (*mirrorMetrics, error) {
	images, err := imagemetrics.New(registerer)
	if err != nil {
		return nil, err
	}
	m := &mirrorMetrics{
		images: images,
		orphanTags: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_mirror_tags_orphan",
			Help: "Destination tags an ImageMirror found that no tracked reference accounts for, held before deletion. " +
				"Counted in tags, so a pinned manifest whose tag and anchor both dangle counts twice",
		}, []string{seriesKind, seriesName}),
		copyFailed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_image_copy_failed",
			Help: "1 while an image cannot be copied to the destination. `image` is the origin reference, the copy being what could not be made",
		}, []string{seriesKind, seriesName, "image", "registry", seriesReason}),
		copies: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kuik_mirror_copies_total",
			Help: "Images pushed to a destination, by why (`Initial`, `Recopy` after a manifest went missing, `Resync` after an upstream digest moved)",
		}, []string{seriesKind, seriesName, seriesReason}),
		deleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kuik_mirror_tags_deleted_total",
			Help: "Destination tags deleted by an ImageMirror, by why they were removed " +
				"(`Unused` once their retention elapsed, `Orphan` when the sweep found a tag no origin accounts for)",
		}, []string{seriesKind, seriesName, seriesReason}),
		// The spec leaves the buckets open: one second to half an hour covers an image of a
		// few layers on a fast link as much as a large index on a slow one.
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kuik_mirror_copy_duration_seconds",
			Help:    "Seconds spent transferring one image to the destination, from the first blob request to the manifest being tagged",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12),
		}, []string{seriesKind, seriesName}),
		selfChecked: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kuik_mirror_self_checked_timestamp_seconds",
			Help: "Unix timestamp at which the last full comparison of the destination finished",
		}, []string{seriesKind, seriesName}),
		failing: map[string]map[[3]string]bool{},
	}
	for _, c := range []prometheus.Collector{m.orphanTags, m.copyFailed, m.copies, m.deleted, m.duration, m.selfChecked} {
		if err := registerer.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// report exports the gauges of one status write of the mirror name.
func (m *mirrorMetrics) report(kind, name string, counts kuikv1alpha1.CopyCounts, failed map[string]copyFailure, drifted []string) {
	m.images.SetCounts(kind, name, imagemetrics.ReferenceCopy, imagemetrics.Counts{
		Running: counts.Running, Standby: counts.Standby, Retained: counts.Retained,
		Available: counts.Available, Unavailable: counts.Unavailable,
	})
	m.images.SetDrifted(kind, name, drifted)
	m.orphanTags.WithLabelValues(kind, name).Set(float64(counts.OrphanTags))

	m.mu.Lock()
	defer m.mu.Unlock()
	current := map[[3]string]bool{}
	for image, failure := range failed {
		labels := [3]string{image, failure.registry, string(failure.reason)}
		current[labels] = true
		m.copyFailed.WithLabelValues(kind, name, labels[0], labels[1], labels[2]).Set(1)
	}
	for labels := range m.failing[name] {
		if !current[labels] {
			m.copyFailed.DeleteLabelValues(kind, name, labels[0], labels[1], labels[2])
		}
	}
	m.failing[name] = current
}

// copied counts one copy of the mirror name, and observes how long it took when enabled.
func (m *mirrorMetrics) copied(kind, name, reason string, took time.Duration, observe bool) {
	m.copies.WithLabelValues(kind, name, reason).Inc()
	if observe {
		m.duration.WithLabelValues(kind, name).Observe(took.Seconds())
	}
}

// deletedTag counts one tag the mirror name deleted: an orphan when it carried no origin.
func (m *mirrorMetrics) deletedTag(kind, name string, entry kuikv1alpha1.PendingDeletion) {
	reason := deletedUnused
	if entry.Origin == "" {
		reason = deletedOrphan
	}
	m.deleted.WithLabelValues(kind, name, reason).Inc()
}

// forget deletes every series of the mirror name.
func (m *mirrorMetrics) forget(kind, name string) {
	labels := prometheus.Labels{seriesKind: kind, seriesName: name}
	m.images.Forget(kind, name)
	m.orphanTags.DeletePartialMatch(labels)
	m.copyFailed.DeletePartialMatch(labels)
	m.copies.DeletePartialMatch(labels)
	m.deleted.DeletePartialMatch(labels)
	m.duration.DeletePartialMatch(labels)
	m.selfChecked.DeletePartialMatch(labels)
	m.mu.Lock()
	delete(m.failing, name)
	m.mu.Unlock()
}
