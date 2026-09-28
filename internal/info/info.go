// Package info holds the build information set at link time.
package info

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

// Version and build information, set at link time.
var (
	Version       = "0.0.0"
	Revision      = ""
	BuildDateTime = ""
)

// NewCollector returns the collector of kuik_build_info, a gauge at 1 labelled with the
// build information. It is an operational metric outside the observability catalogue.
func NewCollector() prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "kuik_build_info",
		Help: "A metric with a constant '1' value labeled with version, revision, build date, " +
			"Go version, Go OS, and Go architecture",
		ConstLabels: prometheus.Labels{
			"version":   Version,
			"revision":  Revision,
			"built":     BuildDateTime,
			"goversion": runtime.Version(),
			"goos":      runtime.GOOS,
			"goarch":    runtime.GOARCH,
		},
	}, func() float64 { return 1 })
}
