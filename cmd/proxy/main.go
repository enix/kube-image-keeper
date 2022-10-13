package main

import (
	"flag"

	"gitlab.enix.io/products/docker-cache-registry/internal/proxy"
	klog "k8s.io/klog/v2"
)

func initFlags() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
}

func main() {
	initFlags()

	klog.Info("Starting")

	<-proxy.New().Listen().Serve()
}
