package metrics

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

const Namespace = "kube_image_keeper"

// Version and build informations set at link time
var Version = "0.0.0"
var Revision = ""
var BuildDateTime = ""

type Info struct {
	metric prometheus.Metric
}

// Describe implements Collector.
func (i *Info) Describe(ch chan<- *prometheus.Desc) {
	ch <- i.metric.Desc()
}

// Collect implements Collector.
func (i *Info) Collect(ch chan<- prometheus.Metric) {
	ch <- i.metric
}

func NewInfo(subsystem string) prometheus.Collector {
	infoMetric := prometheus.BuildFQName(Namespace, subsystem, "build_info")
	infoHelp := "A metric with a constant '1' value labeled with version, revision, build date, Go version, Go OS, and Go architecture"
	infoConstLabels := prometheus.Labels{
		"version":   Version,
		"revision":  Revision,
		"built":     BuildDateTime,
		"goversion": runtime.Version(),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
	}
	infoDesc := prometheus.NewDesc(infoMetric, infoHelp, nil, infoConstLabels)

	return &Info{
		metric: prometheus.MustNewConstMetric(infoDesc, prometheus.GaugeValue, float64(1)),
	}
}
