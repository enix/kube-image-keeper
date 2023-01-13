package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/enix/kube-image-keeper/internal/proxy"
	"github.com/enix/kube-image-keeper/internal/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	klog "k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	kubeconfig string
)

func initFlags() {
	klog.InitFlags(nil)
	if err := flag.Set("logtostderr", "true"); err != nil {
		fmt.Fprint(os.Stderr, "could not enable logging to stderr")
	}
	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")

	flag.Parse()
}

func main() {
	initFlags()

	var config *rest.Config
	var err error

	if kubeconfig == "" {
		klog.Info("using in-cluster configuration")
		config, err = rest.InClusterConfig()
	} else {
		klog.Info("using configuration from '%s'", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	klog.Info("starting")

	if err != nil {
		panic(err)
	}

	k8sClient, err := client.New(config, client.Options{Scheme: scheme.NewScheme()})
	if err != nil {
		panic(err)
	}

	<-proxy.New(k8sClient).Listen().Serve()
}
