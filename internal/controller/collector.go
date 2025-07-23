package controller

import (
	"github.com/enix/kube-image-keeper/internal/info"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type kuikMetrics struct {
	collectors      []prometheus.Collector
	monitoringTasks *prometheus.CounterVec
}

var Metrics kuikMetrics

func (m *kuikMetrics) Register(elected <-chan struct{}) {
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

	metrics.Registry.MustRegister(m.collectors...)
}

func (m *kuikMetrics) addCollector(collector prometheus.Collector) {
	m.collectors = append(m.collectors, collector)
}

func (m *kuikMetrics) MonitoringTaskSucceded(registry string) {
	m.monitoringTasks.WithLabelValues(registry, "success").Inc()
}

func (m *kuikMetrics) MonitoringTaskFailed(registry string) {
	m.monitoringTasks.WithLabelValues(registry, "failure").Inc()
}
