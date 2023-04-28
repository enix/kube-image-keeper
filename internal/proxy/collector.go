package proxy

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

type Collector struct {
	httpCall *prometheus.CounterVec
}

func NewCollector() *Collector {
	return &Collector{
		httpCall: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kube_image_keeper_proxy_http_call",
				Help: "How many HTTP calls have been handled",
			},
			[]string{"method", "endpoint", "statusCode", "cacheHit"},
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.httpCall.Describe(ch)
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.httpCall.Collect(ch)
}

func (c *Collector) IncHTTPCall(request *http.Request, statusCode int, cacheHit bool) {
	c.httpCall.WithLabelValues(request.Method, request.RequestURI, fmt.Sprintf("%d", statusCode), fmt.Sprintf("%t", cacheHit)).Inc()
}
