// Package info holds the build information set at link time.
package info

import "github.com/prometheus/client_golang/prometheus"

// Version and build information, set at link time.
var (
	Version       = "0.0.0"
	Revision      = ""
	BuildDateTime = ""
)

// NewCollector returns the collector of kuik_build_info.
func NewCollector() prometheus.Collector {
	return nil
}
