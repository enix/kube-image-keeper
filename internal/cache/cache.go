package cache

import (
	"context"
	"fmt"

	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Cache struct {
	*kubernetes.Clientset

	podImages map[types.UID]map[string]struct{}
}

func New(k8sClient *kubernetes.Clientset) *Cache {
	return &Cache{
		Clientset: k8sClient,
		podImages: map[types.UID]map[string]struct{}{},
	}
}

func (c *Cache) WatchPods() (chan struct{}, error) {
	podsWatcher, err := c.CoreV1().Pods("default").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	finished := make(chan struct{})
	go func() {
		klog.Info("start watching pods")

		for event := range podsWatcher.ResultChan() {
			pod := event.Object.(*v1.Pod)

			containers := append(pod.Spec.Containers, pod.Spec.InitContainers...)

			klog.V(2).InfoS("pod event", "pod", klog.KObj(pod), "event", event.Type)

			images := map[string]struct{}{}
			for i, container := range containers {
				image, ok := pod.Annotations[fmt.Sprintf("tugger-original-image-%d", i)]
				if !ok {
					klog.V(2).InfoS("missing original image, ignoring", "pod", klog.KObj(pod), "container", container.Name)
					continue
				}
				images[image] = struct{}{}
			}

			switch event.Type {
			// TODO case watch.Modified:
			case watch.Added:
				for image := range images {
					klog.InfoS("caching image", "image", image)
					if cacheUpdated, err := registry.CacheImage(image); err != nil {
						klog.ErrorS(err, "failed to cache image", "image", image)
					} else if cacheUpdated {
						klog.InfoS("image cached", "image", image)
					} else {
						klog.InfoS("image already cached, ignoring", "image", image)
					}
				}

				c.mapPodImage(pod, images)

				break
			case watch.Deleted:
				c.unmapPodImage(pod, images)

				for image := range images {
					if c.isImageInUse(image) {
						klog.InfoS("image still in use, not deleting", "image", image)
					} else {
						klog.InfoS("deleting image from cache", "image", image)
						if err := registry.DeleteImage(image); err != nil {
							klog.ErrorS(err, "failed to delete image from cache", "image", image)
						} else {
							klog.InfoS("image deleted from cache", "image", image)
						}
					}
				}

				break
			default:
				continue
			}
		}

		klog.Info("stop watching pods")
		finished <- struct{}{}
	}()

	return finished, nil
}

func (c *Cache) mapPodImage(pod *v1.Pod, images map[string]struct{}) {
	if _, ok := c.podImages[pod.UID]; !ok {
		c.podImages[pod.UID] = map[string]struct{}{}
	}

	for image := range images {
		c.podImages[pod.UID][image] = struct{}{}
	}
}

func (c *Cache) unmapPodImage(pod *v1.Pod, images map[string]struct{}) {
	if _, ok := c.podImages[pod.UID]; !ok {
		return
	}

	for image := range images {
		delete(c.podImages[pod.UID], image)
	}

	if len(c.podImages[pod.UID]) == 0 {
		delete(c.podImages, pod.UID)
	}
}

func (c *Cache) isImageInUse(image string) bool {
	for _, images := range c.podImages {
		for i := range images {
			if i == image {
				return true
			}
		}
	}

	return false
}
