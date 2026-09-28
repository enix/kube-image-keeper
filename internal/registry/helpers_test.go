package registry

import (
	. "github.com/onsi/ginkgo/v2"

	"errors"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal/registry/registrytest"
)

const (
	user     = "kuik"
	password = "secret"
)

var (
	goodCredential = func() authn.Authenticator { return &authn.Basic{Username: user, Password: password} }
	badCredential  = func() authn.Authenticator { return &authn.Basic{Username: user, Password: "wrong"} }
)

// newRegistry starts a registrytest registry closed at the end of the spec.
func newRegistry(opts ...registrytest.Option) *registrytest.Registry {
	reg := registrytest.New(opts...)
	DeferCleanup(reg.Close)
	return reg
}

// endpoint is an insecure endpoint on reg, the registrytest registries serving plain HTTP.
func endpoint(reg *registrytest.Registry, ref string, auth ...authn.Authenticator) Endpoint {
	return Endpoint{Reference: reg.Host() + "/" + ref, Insecure: true, Auth: auth}
}

func checkReason(err error) kuikv1alpha1.CheckFailureReason {
	if checkErr, ok := errors.AsType[*CheckError](err); ok {
		return checkErr.Reason
	}
	return ""
}

// counted is the value of kuik_registry_requests_total for these labels.
func counted(host, operation, result string) float64 {
	return testutil.ToFloat64(RequestsTotal.WithLabelValues(host, operation, result))
}

// countedFor sums kuik_registry_requests_total over every series of host.
func countedFor(host string) float64 {
	ch := make(chan prometheus.Metric, 100)
	go func() {
		RequestsTotal.Collect(ch)
		close(ch)
	}()
	var total float64
	for metric := range ch {
		var m dto.Metric
		if err := metric.Write(&m); err != nil {
			panic(err)
		}
		for _, label := range m.GetLabel() {
			if label.GetName() == "registry" && label.GetValue() == host {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}
