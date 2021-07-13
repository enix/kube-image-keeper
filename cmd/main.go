package main

import (
	"flag"

	"gitlab.enix.io/products/docker-cache-registry/internal/cache"
	"gitlab.enix.io/products/docker-cache-registry/internal/proxy"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
	klog "k8s.io/klog/v2"
)

var kubeconfig *string

func initFlags() {
	kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")

	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
}

func main() {
	initFlags()

	klog.InfoS("Starting", "inClusterConfig", *kubeconfig == "", "kubeconfig", *kubeconfig)

	k8sClient, err := registry.NewK8sClient(*kubeconfig)
	if err != nil {
		panic(err.Error())
	}
	cacheController := cache.New(k8sClient)

	stopCh := make(chan struct{})
	err = cacheController.WatchPods(stopCh)
	if err != nil {
		panic(err.Error())
	}

	serveFinished := proxy.Serve(cacheController)

	<-serveFinished
	stopCh <- struct{}{}
}
