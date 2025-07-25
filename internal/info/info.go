package info

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

const MetricsNamespace = "kube_image_keeper"

// Version and build informations set at link time
var (
	Version       = "0.0.0"
	Revision      = ""
	BuildDateTime = ""
)

type InfoCollector struct {
	metric prometheus.Metric
}

// Describe implements Collector.
func (i *InfoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- i.metric.Desc()
}

// Collect implements Collector.
func (i *InfoCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- i.metric
}

func NewInfoCollector(subsystem string) prometheus.Collector {
	infoMetric := prometheus.BuildFQName(MetricsNamespace, subsystem, "build_info")
	infoHelp := "A metric with a constant '1' value labeled with version, revision, build date, Go version, Go OS, and Go architecture"
	infoConstLabels := prometheus.Labels(GetInfo())
	infoDesc := prometheus.NewDesc(infoMetric, infoHelp, nil, infoConstLabels)

	return &InfoCollector{
		metric: prometheus.MustNewConstMetric(infoDesc, prometheus.GaugeValue, float64(1)),
	}
}

func GetInfo() map[string]string {
	return map[string]string{
		"version":   Version,
		"revision":  Revision,
		"built":     BuildDateTime,
		"goversion": runtime.Version(),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
	}
}
