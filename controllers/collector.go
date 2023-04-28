package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	imagePutInCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kube_image_keeper_image_put_in_cache",
			Help: "Number of images put in cache successfully",
		},
	)
	imageRemovedFromCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kube_image_keeper_image_removed_from_cache",
			Help: "Number of images removed from cache successfully",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		imagePutInCache,
		imageRemovedFromCache,
	)
}

func IncImagePutInCache() {
	imagePutInCache.Inc()
}

func IncImageRemovedFromCache() {
	imageRemovedFromCache.Inc()
}
