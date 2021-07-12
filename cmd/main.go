package main

import (
	"flag"
	"path/filepath"

	"gitlab.enix.io/products/docker-cache-registry/internal/cache"
	"gitlab.enix.io/products/docker-cache-registry/internal/proxy"
	"gitlab.enix.io/products/docker-cache-registry/internal/registry"
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

func main() {
	initFlags()

	klog.InfoS("Starting", "inClusterConfig", *kubeconfig == "", "kubeconfig", *kubeconfig)

	k8sClient, err := registry.NewK8sClient(*kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	cacheController := cache.New(k8sClient)
	watchFinished, err := cacheController.WatchPods()
	if err != nil {
		panic(err.Error())
	}

	serveFinished := proxy.Serve(cacheController)

	<-watchFinished
	<-serveFinished
}
