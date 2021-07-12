package cache

import (
	"context"
	"errors"
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

	podImages map[types.UID]map[string]*ImageInfo
}

type ImageInfo struct {
	SourceImage string
	Cached      bool
}

func New(k8sClient *kubernetes.Clientset) *Cache {
	return &Cache{
		Clientset: k8sClient,
		podImages: map[types.UID]map[string]*ImageInfo{},
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

			imageInfos := map[string]*ImageInfo{}
			for i, container := range containers {
				sourceImage, ok := pod.Annotations[fmt.Sprintf("tugger-original-image-%d", i)]
				if !ok {
					klog.V(2).InfoS("missing source image, ignoring", "pod", klog.KObj(pod), "container", container.Name)
					continue
				}
				imageInfos[container.Image] = &ImageInfo{
					SourceImage: sourceImage,
				}
			}

			switch event.Type {
			// TODO case watch.Modified:
			case watch.Added:
				c.mapPodImage(pod, imageInfos)

				for _, imageInfo := range imageInfos {
					klog.InfoS("caching image", "image", imageInfo.SourceImage)
					if cacheUpdated, err := registry.CacheImage(imageInfo.SourceImage); err != nil {
						klog.ErrorS(err, "failed to cache image", "image", imageInfo.SourceImage)
						continue
					} else if cacheUpdated {
						klog.InfoS("image cached", "image", imageInfo.SourceImage)
					} else {
						klog.InfoS("image already cached, ignoring", "image", imageInfo.SourceImage)
					}
					imageInfo.Cached = true
				}
				break
			case watch.Deleted:
				c.unmapPodImage(pod, imageInfos)

				for image, imageInfo := range imageInfos {
					if c.isImageInUse(image) {
						klog.InfoS("image still in use, not deleting", "image", imageInfo.SourceImage)
					} else {
						klog.InfoS("deleting image from cache", "image", imageInfo.SourceImage)
						if err := registry.DeleteImage(imageInfo.SourceImage); err != nil {
							klog.ErrorS(err, "failed to delete image from cache", "image", imageInfo.SourceImage)
						} else {
							klog.InfoS("image deleted from cache", "image", imageInfo.SourceImage)
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

func (c *Cache) GetImageInfo(image string) (*ImageInfo, error) {
	for _, images := range c.podImages {
		if imageInfo, ok := images[image]; ok {
			return imageInfo, nil
		}
	}

	return nil, errors.New("image not found")
}

func (c *Cache) mapPodImage(pod *v1.Pod, imageInfos map[string]*ImageInfo) {
	if _, ok := c.podImages[pod.UID]; !ok {
		c.podImages[pod.UID] = map[string]*ImageInfo{}
	}

	for image, imageInfo := range imageInfos {
		c.podImages[pod.UID][image] = imageInfo
	}
}

func (c *Cache) unmapPodImage(pod *v1.Pod, imageInfos map[string]*ImageInfo) {
	if _, ok := c.podImages[pod.UID]; !ok {
		return
	}

	for image := range imageInfos {
		delete(c.podImages[pod.UID], image)
	}

	if len(c.podImages[pod.UID]) == 0 {
		delete(c.podImages, pod.UID)
	}
}

func (c *Cache) isImageInUse(image string) bool {
	imageInfo, err := c.GetImageInfo(image)
	return err == nil && imageInfo.SourceImage != ""
}
