package controllers

import (
	kuikMetrics "github.com/enix/kube-image-keeper/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const subsystem = "controller"

var (
	imagePutInCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_put_in_cache",
			Help:      "Number of images put in cache successfully",
		},
	)
	imageRemovedFromCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_removed_from_cache",
			Help:      "Number of images removed from cache successfully",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		imagePutInCache,
		imageRemovedFromCache,
		kuikMetrics.NewInfo(subsystem),
	)
}

func IncImagePutInCache() {
	imagePutInCache.Inc()
}

func IncImageRemovedFromCache() {
	imageRemovedFromCache.Inc()
}
