package controller

import (
	"context"
	"strconv"

	"github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/controller/core"
	kuikMetrics "github.com/enix/kube-image-keeper/internal/metrics"
	"github.com/enix/kube-image-keeper/internal/registry"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const subsystem = "controller"

var (
	ImageCachingRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: kuikMetrics.Namespace,
			Subsystem: subsystem,
			Name:      "image_caching_request",
			Help:      "Number of request to cache an image",
		},
		[]string{"successful", "upstream_registry"},
	)
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
	Up = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
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

	containersWithCachedImageMetric = prometheus.BuildFQName(kuikMetrics.Namespace, subsystem, "containers_with_cached_image")
	containersWithCachedImageHelp   = "Number of containers that have been rewritten to use a cached image"
	containersWithCachedImageDesc   = prometheus.NewDesc(containersWithCachedImageMetric, containersWithCachedImageHelp, []string{"status", "cached"}, nil)
)

func RegisterMetrics(client client.Client) {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		ImageCachingRequest,
		ImagePutInCache,
		ImageRemovedFromCache,
		kuikMetrics.NewInfo(subsystem),
		isLeader,
		Up,
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
	ch <- containersWithCachedImageDesc
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

	if containersWithCachedImageGaugeVec, err := c.getContainersWithCachedImageMetric(); err == nil {
		containersWithCachedImageGaugeVec.Collect(ch)
	} else {
		log.FromContext(context.Background()).Error(err, "could not collect "+containersWithCachedImageMetric+" metric")
	}
}

func (c *ControllerCollector) getCachedImagesMetric() (*prometheus.GaugeVec, error) {
	cachedImageList := &v1alpha1.CachedImageList{}
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
		cachedImagesGaugeVec.
			WithLabelValues(cachedImage.Status.Phase, strconv.FormatBool(cachedImage.Status.IsCached), strconv.FormatBool(cachedImage.Spec.ExpiresAt != nil)).
			Inc()
	}

	return cachedImagesGaugeVec, nil
}

func (c *ControllerCollector) getContainersWithCachedImageMetric() (*prometheus.GaugeVec, error) {
	cachedImageList := &v1alpha1.CachedImageList{}
	if err := c.List(context.Background(), cachedImageList); err != nil {
		return nil, err
	}

	cachedImages := map[string]v1alpha1.CachedImage{}
	for _, cachedImage := range cachedImageList.Items {
		cachedImages[cachedImage.Name] = cachedImage
	}

	podList := &corev1.PodList{}
	labelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			core.LabelManagedName: "true",
		},
	}
	selector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		return nil, err
	}
	if err := c.List(context.Background(), podList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	containersWithCachedImageGaugeVec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: containersWithCachedImageMetric,
			Help: containersWithCachedImageHelp,
		},
		[]string{"status", "cached"},
	)
	for _, pod := range podList.Items {
		for _, container := range pod.Spec.Containers {
			annotationKey := registry.ContainerAnnotationKey(container.Name, false)
			if sourceImage, ok := pod.ObjectMeta.Annotations[annotationKey]; ok {
				cachedImageName, err := v1alpha1.CachedImageNameFromSourceImage(sourceImage)
				if err != nil {
					return nil, err
				}
				if cachedImage, ok := cachedImages[cachedImageName]; ok {
					containersWithCachedImageGaugeVec.
						WithLabelValues(cachedImage.Status.Phase, strconv.FormatBool(cachedImage.Status.IsCached)).
						Inc()
				}
			}
		}
	}

	return containersWithCachedImageGaugeVec, nil
}

func (c *ControllerCollector) getRepositoriesMetric() (*prometheus.GaugeVec, error) {
	repositoriesList := &v1alpha1.RepositoryList{}
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
		repositoriesGaugeVec.WithLabelValues(repository.Status.Phase).Inc()
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
