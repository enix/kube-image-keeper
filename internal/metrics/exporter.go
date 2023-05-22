package metrics

import (
	"context"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

type Exporter struct {
	address string

	listener  net.Listener
	server    *http.Server
	collector prometheus.Collector
}

func New(collector prometheus.Collector, address string) *Exporter {
	exporter := &Exporter{
		address:   address,
		collector: collector,
	}
	return exporter
}

func (e *Exporter) ListenAndServe() error {
	if err := e.Listen(); err != nil {
		return err
	}

	return e.Serve()
}

func (e *Exporter) Listen() error {
	err := prometheus.Register(e.collector)
	if err != nil {
		if registered, ok := err.(prometheus.AlreadyRegisteredError); ok {
			prometheus.Unregister(registered.ExistingCollector)
			prometheus.MustRegister(e.collector)
		}
	}

	klog.Infof("metrics server listening on %s", e.address)

	listener, err := net.Listen("tcp", e.address)
	if err != nil {
		return err
	}

	e.listener = listener
	return nil
}

func (e *Exporter) Serve() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	e.server = &http.Server{
		Handler: mux,
	}

	return e.server.Serve(e.listener)
}

func (e *Exporter) Shutdown() error {
	return e.server.Shutdown(context.Background())
}
