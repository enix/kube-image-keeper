package controller

import (
	"context"
	"strconv"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"github.com/enix/kube-image-keeper/internal/info"
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

var (
	Metrics kuikMetrics
	log     = logf.Log.WithName("metrics")
)

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

	const subsystemMonitoring = "monitoring"

	m.monitoringTasks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemMonitoring,
			Name:      "tasks_total",
			Help:      "Total number of image monitoring tasks, labeled by registry and status.",
		},
		[]string{"name", "registry", "status", "used"},
	)
	m.addCollector(m.monitoringTasks)

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
				log.Error(err, "failed to list images for metrics")
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

	// imageLastMonitorHistogramOpts := prometheus.Opts{
	// 	Namespace: info.MetricsNamespace,
	// 	Subsystem: subsystemMonitoring,
	// 	Name:      "image_last_monitor_age_minutes",
	// 	Help:      "Histogram of image last monitor age in minutes",
	// }
	// imageLastMonitorHistogramLabels := []string{"registry"}
	// imageLastMonitorHistogramBuckets, err := m.getImageLastMonitorAgeMinutesBuckets()
	// if err != nil {
	// 	// TODO: handle error properly: return it to the caller
	// 	panic(err)
	// }
	// // TODO: refactor NewGenericCollector to avoid duplicating Opts and labels usage
	// m.addCollector(NewGenericCollector(imageLastMonitorHistogramOpts, prometheus.GaugeValue, imageLastMonitorHistogramLabels, func(ch chan<- prometheus.Metric) {
	// 	imageMonitorList := &kuikv1alpha1.ImageMonitorList{}
	// 	if err := client.List(context.Background(), imageMonitorList); err != nil {
	// 		return
	// 	}
	//
	// 	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
	// 		Namespace: imageLastMonitorHistogramOpts.Namespace,
	// 		Subsystem: imageLastMonitorHistogramOpts.Subsystem,
	// 		Name:      imageLastMonitorHistogramOpts.Name,
	// 		Help:      imageLastMonitorHistogramOpts.Help,
	// 		Buckets:   imageLastMonitorHistogramBuckets,
	// 	}, imageLastMonitorHistogramLabels)
	//
	// 	now := time.Now()
	// 	for _, imageMonitor := range imageMonitorList.Items {
	// 		if imageMonitor.Status.Upstream.LastMonitor.IsZero() {
	// 			continue
	// 		}
	// 		histogram.WithLabelValues(imageMonitor.Spec.Registry).Observe(now.Sub(imageMonitor.Status.Upstream.LastMonitor.Time).Minutes())
	// 	}
	//
	// 	histogram.Collect(ch)
	// }))
	//
	metrics.Registry.MustRegister(m.collectors...)
}

func (m *kuikMetrics) addCollector(collector prometheus.Collector) {
	m.collectors = append(m.collectors, collector)
}

func (m *kuikMetrics) InitMonitoringTaskRegistry(cisa *kuikv1alpha1.ClusterImageSetAvailability, registry string) {
	for _, status := range kuikv1alpha1.ImageAvailabilityStatusList {
		if status != kuikv1alpha1.ImageAvailabilityScheduled {
			m.monitoringTasks.WithLabelValues(cisa.Name, registry, status.ToString(), "true").Add(0)
			m.monitoringTasks.WithLabelValues(cisa.Name, registry, status.ToString(), "false").Add(0)
		}
	}
}

func (m *kuikMetrics) MonitoringTaskCompleted(cisa *kuikv1alpha1.ClusterImageSetAvailability, registry string, monitoredImage *kuikv1alpha1.MonitoredImage) {
	m.monitoringTasks.WithLabelValues(cisa.Name, registry, string(monitoredImage.Status), strconv.FormatBool(monitoredImage.UnusedSince == nil || monitoredImage.UnusedSince.IsZero())).Inc()
}

// func (m *kuikMetrics) getImageLastMonitorAgeMinutesBuckets() ([]float64, error) {
// 	// TODO: read this from config instead
// 	envPrefix := "KUIK_METRICS_IMAGE_LAST_MONITOR_AGE_MINUTES_BUCKETS_"
// 	switch bucketsType := getenv.EnvOrDefault(envPrefix+"TYPE", "custom"); bucketsType {
// 	case "exponential":
// 		envPrefix += "EXPONENTIAL_"
// 		start, err := getenv.Env[float64](envPrefix + "START")
// 		if err != nil {
// 			return nil, err
// 		}
// 		factor, err := getenv.Env[float64](envPrefix + "FACTOR")
// 		if err != nil {
// 			return nil, err
// 		}
// 		count, err := getenv.Env[int](envPrefix + "COUNT")
// 		if err != nil {
// 			return nil, err
// 		}
// 		return prometheus.ExponentialBuckets(start, factor, count), nil
// 	case "exponentialRange":
// 		envPrefix += "EXPONENTIAL_RANGE_"
// 		minBucket, err := getenv.Env[float64](envPrefix + "MIN")
// 		if err != nil {
// 			return nil, err
// 		}
// 		maxBucket, err := getenv.Env[float64](envPrefix + "MAX")
// 		if err != nil {
// 			return nil, err
// 		}
// 		count, err := getenv.Env[int](envPrefix + "COUNT")
// 		if err != nil {
// 			return nil, err
// 		}
// 		return prometheus.ExponentialBucketsRange(minBucket, maxBucket, count), nil
// 	case "custom":
// 		buckets, err := getenv.Env[[]float64](envPrefix+"CUSTOM", option.WithSeparator(","))
// 		if err != nil && errors.Is(err, getenv.ErrNotSet) {
// 			return []float64{2, 10}, nil
// 		}
// 		return buckets, err
// 	default:
// 		return nil, fmt.Errorf("invalid %s: %s", envPrefix+"TYPE", bucketsType)
// 	}
// }

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
