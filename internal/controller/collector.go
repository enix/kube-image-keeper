package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/info"
	"github.com/obalunenko/getenv"
	"github.com/obalunenko/getenv/option"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type kuikMetrics struct {
	collectors      []prometheus.Collector
	monitoringTasks *prometheus.CounterVec
}

var Metrics kuikMetrics

func (m *kuikMetrics) Register(elected <-chan struct{}, client client.Client) {
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

	const subsystemMonitoring = "registry_monitor" // FIXME: rename this to "monitoring"

	m.monitoringTasks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemMonitoring,
			Name:      "tasks_total",
			Help:      "Total number of image monitoring tasks, labeled by registry and status.",
		},
		[]string{"registry", "status", "used"},
	)
	m.addCollector(m.monitoringTasks)

	m.addCollector(NewGenericCollectorFunc(
		prometheus.Opts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemMonitoring,
			Name:      "images",
			Help:      "Number of image monitors, labeled by registry and status.",
		},
		prometheus.GaugeValue,
		[]string{"registry", "status", "used"},
		func(collect func(value float64, labels ...string)) {
			imageList := &kuikv1alpha1.ImageList{}
			if err := client.List(context.Background(), imageList); err != nil {
				logf.Log.Error(err, "failed to list images for metrics")
				return
			}

			areImagesUsed := map[string]bool{}
			for _, image := range imageList.Items {
				areImagesUsed[image.Reference()] = image.IsUsedByPods()
			}

			imageMonitorList := &kuikv1alpha1.ImageMonitorList{}
			if err := client.List(context.Background(), imageMonitorList); err != nil {
				logf.Log.Error(err, "failed to list images monitors for metrics")
				return
			}

			imageMonitors := make(map[string]map[string]map[bool]float64)
			for _, imageMonitor := range imageMonitorList.Items {
				registry := imageMonitor.Spec.Registry
				if _, exists := imageMonitors[registry]; !exists {
					imageMonitors[registry] = make(map[string]map[bool]float64)
					for _, status := range kuikv1alpha1.ImageMonitorStatusUpstreamList {
						imageMonitors[registry][status.ToString()] = map[bool]float64{
							true:  0,
							false: 0,
						}
					}
				}

				status := imageMonitor.Status.Upstream.Status.ToString()
				imageMonitors[registry][status][areImagesUsed[imageMonitor.Reference()]]++
			}

			for registry, statuses := range imageMonitors {
				for status, used := range statuses {
					collect(used[true], registry, status, strconv.FormatBool(true))
					collect(used[false], registry, status, strconv.FormatBool(false))
				}
			}
		}))

	imageLastMonitorHistogramOpts := prometheus.Opts{
		Namespace: info.MetricsNamespace,
		Subsystem: subsystemMonitoring,
		Name:      "image_last_monitor_age_minutes",
		Help:      "Histogram of image last monitor age in minutes",
	}
	imageLastMonitorHistogramLabels := []string{"registry"}
	imageLastMonitorHistogramBuckets, err := m.getImageLastMonitorAgeMinutesBuckets()
	if err != nil {
		// TODO: handle error properly: return it to the caller
		panic(err)
	}
	// TODO: refactor NewGenericCollector to avoid duplicating Opts and labels usage
	m.addCollector(NewGenericCollector(imageLastMonitorHistogramOpts, prometheus.GaugeValue, imageLastMonitorHistogramLabels, func(ch chan<- prometheus.Metric) {
		imageMonitorList := &kuikv1alpha1.ImageMonitorList{}
		if err := client.List(context.Background(), imageMonitorList); err != nil {
			return
		}

		histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: imageLastMonitorHistogramOpts.Namespace,
			Subsystem: imageLastMonitorHistogramOpts.Subsystem,
			Name:      imageLastMonitorHistogramOpts.Name,
			Help:      imageLastMonitorHistogramOpts.Help,
			Buckets:   imageLastMonitorHistogramBuckets,
		}, imageLastMonitorHistogramLabels)

		now := time.Now()
		for _, imageMonitor := range imageMonitorList.Items {
			if imageMonitor.Status.Upstream.LastMonitor.IsZero() {
				continue
			}
			histogram.WithLabelValues(imageMonitor.Spec.Registry).Observe(now.Sub(imageMonitor.Status.Upstream.LastMonitor.Time).Minutes())
		}

		histogram.Collect(ch)
	}))

	metrics.Registry.MustRegister(m.collectors...)
}

func (m *kuikMetrics) addCollector(collector prometheus.Collector) {
	m.collectors = append(m.collectors, collector)
}

func (m *kuikMetrics) InitMonitoringTaskRegistry(registry string) {
	for _, status := range kuikv1alpha1.ImageMonitorStatusUpstreamList {
		if status != kuikv1alpha1.ImageMonitorStatusUpstreamScheduled {
			m.monitoringTasks.WithLabelValues(registry, status.ToString(), "true").Add(0)
			m.monitoringTasks.WithLabelValues(registry, status.ToString(), "false").Add(0)
		}
	}
}

func (m *kuikMetrics) MonitoringTaskCompleted(registry string, imageIsUsed bool, imageMonitor *kuikv1alpha1.ImageMonitor) {
	m.monitoringTasks.WithLabelValues(registry, imageMonitor.Status.Upstream.Status.ToString(), strconv.FormatBool(imageIsUsed)).Inc()
}

func (m *kuikMetrics) getImageLastMonitorAgeMinutesBuckets() ([]float64, error) {
	envPrefix := "KUIK_METRICS_IMAGE_LAST_MONITOR_AGE_MINUTES_BUCKETS_"
	switch bucketsType := getenv.EnvOrDefault(envPrefix+"TYPE", "custom"); bucketsType {
	case "exponential":
		envPrefix += "EXPONENTIAL_"
		start, err := getenv.Env[float64](envPrefix + "START")
		if err != nil {
			return nil, err
		}
		factor, err := getenv.Env[float64](envPrefix + "FACTOR")
		if err != nil {
			return nil, err
		}
		count, err := getenv.Env[int](envPrefix + "COUNT")
		if err != nil {
			return nil, err
		}
		return prometheus.ExponentialBuckets(start, factor, count), nil
	case "exponentialRange":
		envPrefix += "EXPONENTIAL_RANGE_"
		minBucket, err := getenv.Env[float64](envPrefix + "MIN")
		if err != nil {
			return nil, err
		}
		maxBucket, err := getenv.Env[float64](envPrefix + "MAX")
		if err != nil {
			return nil, err
		}
		count, err := getenv.Env[int](envPrefix + "COUNT")
		if err != nil {
			return nil, err
		}
		return prometheus.ExponentialBucketsRange(minBucket, maxBucket, count), nil
	case "custom":
		buckets, err := getenv.Env[[]float64](envPrefix+"CUSTOM", option.WithSeparator(","))
		if err != nil && errors.Is(err, getenv.ErrNotSet) {
			return []float64{2, 10}, nil
		}
		return buckets, err
	default:
		return nil, fmt.Errorf("invalid %s: %s", envPrefix+"TYPE", bucketsType)
	}
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
