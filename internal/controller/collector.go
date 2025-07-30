package controller

import (
	"context"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
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

	const subsystemRegistryMonitor = "registry_monitor"

	m.monitoringTasks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemRegistryMonitor,
			Name:      "tasks_total",
			Help:      "Total number of image monitoring tasks, labeled by registry and status.",
		},
		[]string{"registry", "status"},
	)
	m.addCollector(m.monitoringTasks)

	m.addCollector(NewGenericCollector(
		prometheus.Opts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemRegistryMonitor,
			Name:      "images",
			Help:      "Number of images monitored, labeled by registry and status.",
		},
		prometheus.GaugeValue,
		[]string{"registry", "status"},
		func(collect func(value float64, labels ...string)) {
			imageList := &kuikv1alpha1.ImageList{}
			if err := client.List(context.Background(), imageList); err != nil {
				logf.Log.Error(err, "failed to list images for metrics")
				return
			}

			images := make(map[string]map[string]float64)
			for _, image := range imageList.Items {
				registry := image.Spec.Registry
				if _, exists := images[registry]; !exists {
					images[registry] = make(map[string]float64)
					for _, status := range kuikv1alpha1.ImageStatusUpstreamList {
						images[registry][status.ToString()] = 0
					}
				}

				status := image.Status.Upstream.Status.ToString()
				if _, exists := images[registry][status]; !exists {
					images[registry][status] = 0
				}

				images[registry][status]++
			}

			for registry, statuses := range images {
				for status, count := range statuses {
					collect(count, registry, status)
				}
			}
		}))

	m.addCollector(NewGenericCollector(
		prometheus.Opts{
			Namespace: info.MetricsNamespace,
			Subsystem: subsystemRegistryMonitor,
			Name:      "registries",
			Help:      "Number of registries monitored up and running, labeled by registry.",
		},
		prometheus.GaugeValue,
		[]string{"registry"},
		func(collect func(value float64, labels ...string)) {
			registryMonitorList := &kuikv1alpha1.RegistryMonitorList{}
			if err := client.List(context.Background(), registryMonitorList); err != nil {
				logf.Log.Error(err, "failed to list registry monitors for metrics")
				return
			}

			for _, registry := range registryMonitorList.Items {
				if registry.Status.RegistryStatus == kuikv1alpha1.RegistryStatusUp {
					collect(1, registry.Name)
				} else {
					collect(0, registry.Name)
				}
			}
		}))

	metrics.Registry.MustRegister(m.collectors...)
}

func (m *kuikMetrics) addCollector(collector prometheus.Collector) {
	m.collectors = append(m.collectors, collector)
}

func (m *kuikMetrics) InitMonitoringTaskRegistry(registry string) {
	for _, status := range kuikv1alpha1.ImageStatusUpstreamList {
		if status != kuikv1alpha1.ImageStatusUpstreamScheduled {
			m.monitoringTasks.WithLabelValues(registry, status.ToString()).Add(0)
		}
	}
}

func (m *kuikMetrics) MonitoringTaskCompleted(registry string, status kuikv1alpha1.ImageStatusUpstream) {
	m.monitoringTasks.WithLabelValues(registry, status.ToString()).Inc()
}

type GenericCollector struct {
	desc              *prometheus.Desc
	valueType         prometheus.ValueType
	collectorCallback func(collect func(value float64, labels ...string))
}

func (g *GenericCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- g.desc
}

func (g *GenericCollector) Collect(ch chan<- prometheus.Metric) {
	g.collectorCallback(func(value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(g.desc, g.valueType, value, labels...)
	})
}

func NewGenericCollector(opts prometheus.Opts, valueType prometheus.ValueType, labels []string, collectorCallback func(collect func(value float64, labels ...string))) prometheus.Collector {
	desc := prometheus.NewDesc(
		prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
		opts.Help,
		labels,
		opts.ConstLabels,
	)

	return &GenericCollector{
		desc:              desc,
		valueType:         valueType,
		collectorCallback: collectorCallback,
	}
}
