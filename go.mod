module github.com/adisplayname/kube-image-keeper

go 1.22

require (
	github.com/awslabs/amazon-ecr-credential-helper/ecr-login v0.0.0-20230519004202-7f2db5bd753e
	github.com/distribution/reference v0.6.0
	github.com/docker/cli v27.1.1+incompatible
	github.com/docker/docker v27.1.1+incompatible
	github.com/docker/go-connections v0.5.0
	github.com/gin-gonic/gin v1.10.0
	github.com/go-logr/logr v1.4.2
	github.com/google/go-containerregistry v0.20.2
	github.com/onsi/ginkgo/v2 v2.20.0
	github.com/onsi/gomega v1.34.1
	github.com/prometheus/client_golang v1.19.1
	go.uber.org/automaxprocs v1.5.3
	go.uber.org/zap v1.27.0
	golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56
	k8s.io/api v0.27.13
	k8s.io/apimachinery v0.27.13
	k8s.io/client-go v0.27.13
	k8s.io/klog/v2 v2.130.1
	k8s.io/kubernetes v1.27.13
	k8s.io/utils v0.0.0-20230726121419-3b25d923346b
	sigs.k8s.io/controller-runtime v0.15.0
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/aws/aws-sdk-go-v2 v1.26.1 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.27.11 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.11 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecr v1.27.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecrpublic v1.23.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.20.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.23.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.28.6 // indirect
	github.com/aws/smithy-go v1.20.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/bytedance/sonic v1.11.6 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.14.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.8.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.3 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.1 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.20.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.16.5 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0-rc3 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.48.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/vbatts/tar-split v0.11.3 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.46.1 // indirect
	go.opentelemetry.io/otel v1.26.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.26.0 // indirect
	go.opentelemetry.io/otel/metric v1.26.0 // indirect
	go.opentelemetry.io/otel/sdk v1.26.0 // indirect
	go.opentelemetry.io/otel/trace v1.26.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/crypto v0.26.0 // indirect
	golang.org/x/mod v0.20.0 // indirect
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/oauth2 v0.16.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.23.0 // indirect
	golang.org/x/term v0.23.0 // indirect
	golang.org/x/text v0.17.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.24.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.27.13 // indirect
	k8s.io/apiserver v0.27.13 // indirect
	k8s.io/component-base v0.27.13 // indirect
	k8s.io/kube-openapi v0.0.0-20230501164219-8b0f38b5fd1f // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.27.13
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.27.13
	k8s.io/apimachinery => k8s.io/apimachinery v0.27.13
	k8s.io/apiserver => k8s.io/apiserver v0.27.13
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.27.13
	k8s.io/client-go => k8s.io/client-go v0.27.13
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.27.13
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.27.13
	k8s.io/code-generator => k8s.io/code-generator v0.27.13
	k8s.io/component-base => k8s.io/component-base v0.27.13
	k8s.io/component-helpers => k8s.io/component-helpers v0.27.13
	k8s.io/controller-manager => k8s.io/controller-manager v0.27.13
	k8s.io/cri-api => k8s.io/cri-api v0.27.13
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.27.13
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.27.13
	k8s.io/endpointslice => k8s.io/endpointslice v0.27.13
	k8s.io/kms => k8s.io/kms v0.27.13
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.27.13
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.27.13
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.27.13
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.27.13
	k8s.io/kubectl => k8s.io/kubectl v0.27.13
	k8s.io/kubelet => k8s.io/kubelet v0.27.13
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.27.13
	k8s.io/metrics => k8s.io/metrics v0.27.13
	k8s.io/mount-utils => k8s.io/mount-utils v0.27.13
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.27.13
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.27.13
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.27.13
	k8s.io/sample-controller => k8s.io/sample-controller v0.27.13
)
