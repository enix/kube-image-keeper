package controller

import (
	"context"
	"strconv"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	kuikMetrics "github.com/enix/kube-image-keeper/internal/metrics"
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
	cachedImagesDesc   = prometheus.NewDesc(cachedImagesMetric, cachedImagesHelp, []string{"status", "cached", "expiring"}, nil)

	repositoriesMetric = prometheus.BuildFQName(kuikMetrics.Namespace, subsystem, "repositories")
	repositoriesHelp   = "Number of repositories"
	repositoriesDesc   = prometheus.NewDesc(repositoriesMetric, repositoriesHelp, []string{"status"}, nil)
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

type ControllerCollector struct {
	client.Client
}

func (c *ControllerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cachedImagesDesc
	ch <- repositoriesDesc
}

func (c *ControllerCollector) Collect(ch chan<- prometheus.Metric) {
	if cachedImagesGaugeVec, err := c.getCachedImagesMetric(); err == nil {
		cachedImagesGaugeVec.Collect(ch)
	} else {
		log.FromContext(context.Background()).Error(err, "could not collect "+cachedImagesMetric+" metric")
	}

	if repositoriesGaugeVec, err := c.getRepositoriesMetric(); err == nil {
		repositoriesGaugeVec.Collect(ch)
	} else {
		log.FromContext(context.Background()).Error(err, "could not collect "+repositoriesMetric+" metric")
	}
}

func (c *ControllerCollector) getCachedImagesMetric() (*prometheus.GaugeVec, error) {
	cachedImageList := &kuikv1alpha1.CachedImageList{}
	if err := c.List(context.Background(), cachedImageList); err != nil {
		return nil, err
	}

	cachedImagesGaugeVec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: cachedImagesMetric,
			Help: cachedImagesHelp,
		},
		[]string{"status", "cached", "expiring"},
	)
	for _, cachedImage := range cachedImageList.Items {
		cachedImagesGauge := cachedImagesGaugeVec.WithLabelValues(cachedImage.Status.Phase, strconv.FormatBool(cachedImage.Status.IsCached), strconv.FormatBool(cachedImage.Spec.ExpiresAt != nil))
		cachedImagesGauge.Inc()
	}

	return cachedImagesGaugeVec, nil
}

func (c *ControllerCollector) getRepositoriesMetric() (*prometheus.GaugeVec, error) {
	repositoriesList := &kuikv1alpha1.RepositoryList{}
	if err := c.List(context.Background(), repositoriesList); err != nil {
		return nil, err
	}

	repositoriesGaugeVec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: repositoriesMetric,
			Help: repositoriesHelp,
		},
		[]string{"status"},
	)
	for _, repository := range repositoriesList.Items {
		repositoriesGauge := repositoriesGaugeVec.WithLabelValues(repository.Status.Phase)
		repositoriesGauge.Inc()
	}

	return repositoriesGaugeVec, nil
}

func SetLeader(leader bool) {
	if leader {
		isLeader.Set(1)
	} else {
		isLeader.Set(0)
	}
}
