package controllers

import (
	"context"
	"strconv"

	kuikenixiov1alpha1 "github.com/enix/kube-image-keeper/api/v1alpha1"
	kuikMetrics "github.com/enix/kube-image-keeper/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const subsystem = "controller"

var (
	imagePutInCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_put_in_cache_total",
			Help:      "Number of images put in cache successfully",
		},
	)
	imageRemovedFromCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_removed_from_cache_total",
			Help:      "Number of images removed from cache successfully",
		},
	)
	cachedImagesMetric = prometheus.BuildFQName(kuikMetrics.Namespace, subsystem, "cached_images")
	cachedImagesHelp   = "Number of images expected to be cached"
	cachedImagesDesc   = prometheus.NewDesc(cachedImagesMetric, cachedImagesHelp, []string{"cached", "expiring"}, nil)
)

func RegisterMetrics(client client.Client) {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		imagePutInCache,
		imageRemovedFromCache,
		kuikMetrics.NewInfo(subsystem),
		&ControllerCollector{
			Client: client,
		},
	)
}

func cachedImagesWithLabelValues(gaugeVec *prometheus.GaugeVec, cachedImage *kuikenixiov1alpha1.CachedImage) prometheus.Gauge {
	return gaugeVec.WithLabelValues(strconv.FormatBool(cachedImage.Status.IsCached), strconv.FormatBool(cachedImage.Spec.ExpiresAt != nil))
}

type ControllerCollector struct {
	client.Client
}

func (c *ControllerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cachedImagesDesc
}

func (c *ControllerCollector) Collect(ch chan<- prometheus.Metric) {
	cachedImageList := &kuikenixiov1alpha1.CachedImageList{}
	if err := c.List(context.Background(), cachedImageList); err == nil {
		cachedImageGaugeVec := prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: cachedImagesMetric,
				Help: cachedImagesHelp,
			},
			[]string{"cached", "expiring"},
		)
		for _, cachedImage := range cachedImageList.Items {
			cachedImagesWithLabelValues(cachedImageGaugeVec, &cachedImage).Inc()
		}
		cachedImageGaugeVec.Collect(ch)
	} else {
		log.FromContext(context.TODO()).Error(err, "could not collect "+cachedImagesMetric+" metric")
	}
}
