package cache

import (
	"errors"
	"fmt"
	"time"

	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"
)

type Cache struct {
	*kubernetes.Clientset

	podImages       map[types.UID]map[string]*ImageInfo
	informerFactory informers.SharedInformerFactory
	podInformer     coreinformers.PodInformer
}

type ImageInfo struct {
	SourceImage string
	Cached      bool
}

func New(k8sClient *kubernetes.Clientset) *Cache {
	informerFactory := informers.NewSharedInformerFactory(k8sClient, time.Hour*24)
	podInformer := informerFactory.Core().V1().Pods()

	c := &Cache{
		Clientset:       k8sClient,
		podImages:       map[types.UID]map[string]*ImageInfo{},
		informerFactory: informerFactory,
		podInformer:     podInformer,
	}

	podInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.podAdd,
			UpdateFunc: c.podUpdate,
			DeleteFunc: c.podDelete,
		},
	)

	return c
}

func (c *Cache) GetImageInfo(image string) (*ImageInfo, error) {
	for _, images := range c.podImages {
		if imageInfo, ok := images[image]; ok {
			return imageInfo, nil
		}
	}

	return nil, errors.New("image not found")
}

func (c *Cache) WatchPods(stopCh chan struct{}) error {
	c.informerFactory.Start(stopCh)
	if !cache.WaitForCacheSync(stopCh, c.podInformer.Informer().HasSynced) {
		return fmt.Errorf("Failed to sync")
	}
	return nil
}

func (c *Cache) podAdd(obj interface{}) {
	pod := obj.(*v1.Pod)
	klog.V(2).InfoS("pod created", "pod", klog.KObj(pod))

	imageInfos := c.getImageInfosFromPod(pod)
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
}

func (c *Cache) podUpdate(old, new interface{}) {
	// oldPod := old.(*v1.Pod)
	newPod := new.(*v1.Pod)

	klog.V(2).InfoS("pod updated", "pod", klog.KObj(newPod))
}

func (c *Cache) podDelete(obj interface{}) {
	pod := obj.(*v1.Pod)
	klog.V(2).InfoS("pod deleted", "pod", klog.KObj(pod))

	imageInfos := c.getImageInfosFromPod(pod)
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
}

func (c *Cache) getImageInfosFromPod(pod *v1.Pod) map[string]*ImageInfo {
	containers := append(pod.Spec.Containers, pod.Spec.InitContainers...)

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

	return imageInfos
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
