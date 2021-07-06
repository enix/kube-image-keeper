package main

import (
	"context"
	"flag"
	"path/filepath"

	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	klog "k8s.io/klog/v2"
)

var kubeconfig *string

func initFlags() {
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
}

func getK8sClientConfig() (*rest.Config, error) {
	if *kubeconfig == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}

		return config, nil
	}

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func newK8sClient() (*kubernetes.Clientset, error) {
	config, err := getK8sClientConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

var podImages = map[types.UID]map[string]struct{}{}

func mapPodImage(pod *v1.Pod, images []string) {
	if _, ok := podImages[pod.UID]; !ok {
		podImages[pod.UID] = map[string]struct{}{}
	}

	for _, image := range images {
		podImages[pod.UID][image] = struct{}{}
	}
}

func unmapPodImage(pod *v1.Pod, images []string) {
	if _, ok := podImages[pod.UID]; !ok {
		return
	}

	for _, image := range images {
		delete(podImages[pod.UID], image)
	}

	if len(podImages[pod.UID]) == 0 {
		delete(podImages, pod.UID)
	}
}

func isImageInUse(image string) bool {
	for _, images := range podImages {
		for i := range images {
			if i == image {
				return true
			}
		}
	}

	return false
}

func main() {
	initFlags()

	klog.InfoS("Starting", "inClusterConfig", *kubeconfig == "", "kubeconfig", *kubeconfig)

	k8sClient, err := newK8sClient()
	if err != nil {
		panic(err.Error())
	}

	podsWatcher, err := k8sClient.CoreV1().Pods("default").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	finished := make(chan struct{})
	go func() {
		klog.Info("start watching pods")

		for event := range podsWatcher.ResultChan() {
			pod := event.Object.(*v1.Pod)

			containers := append(pod.Spec.Containers, pod.Spec.InitContainers...)

			klog.InfoS("pod event", "pod", klog.KObj(pod), "event", event.Type)

			var images []string
			for _, container := range containers {
				images = append(images, container.Image)
			}

			switch event.Type {
			// TODO case watch.Modified:
			case watch.Added:
				for _, image := range images {
					klog.InfoS("caching image", "image", image)
					if cacheUpdated, err := registry.CacheImage(image); err != nil {
						klog.ErrorS(err, "failed to cache image", "image", image)
					} else if cacheUpdated {
						klog.InfoS("image cached", "image", image)
					} else {
						klog.InfoS("image already cached, ignoring", "image", image)
					}
				}

				mapPodImage(pod, images)

				break
			case watch.Deleted:
				unmapPodImage(pod, images)

				for _, image := range images {
					if isImageInUse(image) {
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

	<-finished
}
