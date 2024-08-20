package proxy

import (
	"fmt"

	"github.com/adisplayname/kube-image-keeper/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

const subsystem = "proxy"

type Collector struct {
	httpCall *prometheus.CounterVec
	info     prometheus.Collector
}

func NewCollector() *Collector {
	return &Collector{
		httpCall: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: metrics.Namespace,
				Subsystem: subsystem,
				Name:      "http_requests_total",
				Help:      "How many HTTP calls have been handled",
			},
			[]string{"registry", "statusCode", "cacheHit"},
		),
		info: metrics.NewInfo(subsystem),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.httpCall.Describe(ch)
	c.info.Describe(ch)
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.httpCall.Collect(ch)
	c.info.Collect(ch)
}

func (c *Collector) IncHTTPCall(registry string, statusCode int, cacheHit bool) {
	c.httpCall.WithLabelValues(registry, fmt.Sprintf("%d", statusCode), fmt.Sprintf("%t", cacheHit)).Inc()
}
