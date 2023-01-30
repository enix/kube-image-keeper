module github.com/enix/kube-image-keeper

go 1.16

require (
	github.com/docker/cli v20.10.12+incompatible
	github.com/docker/distribution v2.8.1+incompatible
	github.com/docker/docker v20.10.23+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/gin-gonic/gin v1.7.3
	github.com/go-logr/logr v0.4.0
	github.com/google/go-containerregistry v0.6.0
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.15.0
	k8s.io/api v0.21.4
	k8s.io/apimachinery v0.21.4
	k8s.io/client-go v0.21.4
	k8s.io/klog/v2 v2.8.0
	sigs.k8s.io/controller-runtime v0.9.7
)
