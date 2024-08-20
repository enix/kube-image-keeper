package controller

import (
	"context"
	"strconv"

	kuikv1alpha1 "github.com/adisplayname/kube-image-keeper/api/kuik/v1alpha1ext1"
	kuikMetrics "github.com/adisplayname/kube-image-keeper/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const subsystem = "controller"

var ProbeAddr = ""

var (
	ImagePutInCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_put_in_cache_total",
			Help:      "Number of images put in cache successfully",
		},
	)
	ImageRemovedFromCache = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_removed_from_cache_total",
			Help:      "Number of images removed from cache successfully",
		},
	)
	isLeader = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: kuikMetrics.Namespace,
		Subsystem: subsystem,
		Name:      "is_leader",
		Help:      "Whether or not this replica is a leader. 1 if it is, 0 otherwise.",
	})
	up = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: kuikMetrics.Namespace,
		Subsystem: subsystem,
		Name:      "up",
		Help:      "Whether or not this replica is healthy.",
	}, func() float64 {
		if err := Healthz(); err != nil {
			return 0
		}
		return 1
	})

	cachedImagesMetric = prometheus.BuildFQName(kuikMetrics.Namespace, subsystem, "cached_images")
	cachedImagesHelp   = "Number of images expected to be cached"
	cachedImagesDesc   = prometheus.NewDesc(cachedImagesMetric, cachedImagesHelp, []string{"cached", "expiring"}, nil)
)

func RegisterMetrics(client client.Client) {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		ImagePutInCache,
		ImageRemovedFromCache,
		kuikMetrics.NewInfo(subsystem),
		isLeader,
		up,
		&ControllerCollector{
			Client: client,
		},
	)
}

func cachedImagesWithLabelValues(gaugeVec *prometheus.GaugeVec, cachedImage *kuikv1alpha1.CachedImage) prometheus.Gauge {
	return gaugeVec.WithLabelValues(strconv.FormatBool(cachedImage.Status.IsCached), strconv.FormatBool(cachedImage.Spec.ExpiresAt != nil))
}

type ControllerCollector struct {
	client.Client
}

func (c *ControllerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cachedImagesDesc
}

func (c *ControllerCollector) Collect(ch chan<- prometheus.Metric) {
	cachedImageList := &kuikv1alpha1.CachedImageList{}
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

func SetLeader(leader bool) {
	if leader {
		isLeader.Set(1)
	} else {
		isLeader.Set(0)
	}
}
