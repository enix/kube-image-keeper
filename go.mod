module github.com/enix/kube-image-keeper

go 1.16

require (
	github.com/docker/cli v20.10.20+incompatible
	github.com/docker/distribution v2.8.1+incompatible
	github.com/docker/docker v20.10.23+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/gin-gonic/gin v1.7.3
	github.com/go-logr/logr v1.2.0
	github.com/google/go-containerregistry v0.13.0
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.17.0
	k8s.io/api v0.23.5
	k8s.io/apimachinery v0.23.5
	k8s.io/client-go v0.23.5
	k8s.io/klog/v2 v2.30.0
	sigs.k8s.io/controller-runtime v0.11.2
)
