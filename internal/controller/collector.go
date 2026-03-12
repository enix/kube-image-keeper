package controller

import (
	"context"
	"strconv"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/info"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type kuikMetrics struct {
	collectors []prometheus.Collector
}

var (
	Metrics kuikMetrics
	log     = logf.Log.WithName("metrics")
)

func (m *kuikMetrics) Register(elected <-chan struct{}, client client.Client, cfg *config.Config) {
	const subsystemManager = "manager"

	m.addCollector(info.NewInfoCollector(subsystemManager))

	m.addCollector(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: info.MetricsNamespace,
		Subsystem: subsystemManager,
		Name:      "is_leader",
		Help:      "Whether or not this replica is a leader. 1 if it is, 0 otherwise.",
	}, func() float64 {
		select {
		case <-elected:
			return 1
		default:
			return 0
		}
	}))

	m.addCollector(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: info.MetricsNamespace,
		Subsystem: subsystemManager,
		Name:      "up",
		Help:      "Whether or not this replica is healthy.",
	}, func() float64 {
		if err := healthz.Ping(nil); err != nil {
			return 0
		}
		return 1
	}))

	const subsystemMonitoring = "monitoring"

	m.addCollector(NewGenericCollectorFunc(
		prometheus.Opts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemMonitoring,
			Name:      "images",
			Help:      "Number of monitored image",
		},
		prometheus.GaugeValue,
		[]string{"name", "registry", "status", "used"},
		func(collect func(value float64, labels ...string)) {
			cisaList := &kuikv1alpha1.ClusterImageSetAvailabilityList{}
			if err := client.List(context.Background(), cisaList); err != nil {
				log.Error(err, "failed to list images for images metric")
				return
			}

			type counterKey struct {
				name, registry string
				status         kuikv1alpha1.ImageAvailabilityStatus
				used           bool
			}
			counts := map[counterKey]float64{}

			for _, cisa := range cisaList.Items {
				for _, image := range cisa.Status.Images {
					registry, _, err := internal.RegistryAndPathFromReference(image.Image)
					if err != nil {
						continue
					}
					used := image.UnusedSince == nil || image.UnusedSince.IsZero()
					counts[counterKey{
						name:     cisa.Name,
						registry: registry,
						status:   image.Status,
						used:     used,
					}]++
				}
			}

			for key, count := range counts {
				collect(count, key.name, key.registry, string(key.status), strconv.FormatBool(key.used))
			}
		}))

	imageLastMonitorHistogramOpts := prometheus.Opts{
		Namespace: info.MetricsNamespace,
		Subsystem: subsystemMonitoring,
		Name:      "image_last_monitor_age_minutes",
		Help:      "Histogram of image last monitor age in minutes.",
	}
	imageLastMonitorHistogramLabels := []string{"name", "registry"}
	imageLastMonitorHistogram := cfg.Metrics.ImageLastMonitorAgeMinutes
	imageLastMonitorHistogramBuckets := imageLastMonitorHistogram.Legacy.Buckets()

	m.addCollector(NewGenericCollector(imageLastMonitorHistogramOpts, prometheus.GaugeValue, imageLastMonitorHistogramLabels, func(ch chan<- prometheus.Metric) {
		cisaList := &kuikv1alpha1.ClusterImageSetAvailabilityList{}
		if err := client.List(context.Background(), cisaList); err != nil {
			log.Error(err, "failed to list images for last monitor histogram metric")
			return
		}

		histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:                      imageLastMonitorHistogramOpts.Namespace,
			Subsystem:                      imageLastMonitorHistogramOpts.Subsystem,
			Name:                           imageLastMonitorHistogramOpts.Name,
			Help:                           imageLastMonitorHistogramOpts.Help,
			NativeHistogramBucketFactor:    imageLastMonitorHistogram.BucketFactor,
			NativeHistogramZeroThreshold:   imageLastMonitorHistogram.ZeroThreshold,
			NativeHistogramMaxBucketNumber: imageLastMonitorHistogram.MaxBucketNumber,
			Buckets:                        imageLastMonitorHistogramBuckets,
		}, imageLastMonitorHistogramLabels)

		now := time.Now()
		for _, cisa := range cisaList.Items {
			for _, image := range cisa.Status.Images {
				if image.LastMonitor == nil || image.LastMonitor.IsZero() {
					continue
				}
				registry, _, err := internal.RegistryAndPathFromReference(image.Image)
				if err != nil {
					continue
				}
				histogram.WithLabelValues(cisa.Name, registry).Observe(now.Sub(image.LastMonitor.Time).Minutes())
			}
		}

		histogram.Collect(ch)
	}))

	metrics.Registry.MustRegister(m.collectors...)
}

func (m *kuikMetrics) addCollector(collector prometheus.Collector) {
	m.collectors = append(m.collectors, collector)
}

type GenericCollector struct {
	desc      *prometheus.Desc
	valueType prometheus.ValueType
	callback  func(ch chan<- prometheus.Metric)
}

func (g *GenericCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- g.desc
}

func (g *GenericCollector) Collect(ch chan<- prometheus.Metric) {
	g.callback(ch)
}

func NewEmptyGenericCollector(opts prometheus.Opts, valueType prometheus.ValueType, labels []string) *GenericCollector {
	desc := prometheus.NewDesc(
		prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
		opts.Help,
		labels,
		opts.ConstLabels,
	)

	return &GenericCollector{
		desc:      desc,
		valueType: valueType,
	}
}

func NewGenericCollector(opts prometheus.Opts, valueType prometheus.ValueType, labels []string, collectorCallback func(ch chan<- prometheus.Metric)) prometheus.Collector {
	collector := NewEmptyGenericCollector(opts, valueType, labels)
	collector.callback = collectorCallback
	return collector
}

func NewGenericCollectorFunc(opts prometheus.Opts, valueType prometheus.ValueType, labels []string, callback func(collect func(value float64, labels ...string))) prometheus.Collector {
	collector := NewGenericCollector(opts, valueType, labels, nil).(*GenericCollector)

	collector.callback = func(ch chan<- prometheus.Metric) {
		callback(func(value float64, labels ...string) {
			ch <- prometheus.MustNewConstMetric(collector.desc, collector.valueType, value, labels...)
		})
	}

	return collector
}
