package registry

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ctx = context.Background()

func TestRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Registry Suite")
}

// RequestsTotal is labelled by host:port and the kernel reuses the ports of closed test
// registries: without a reset, a spec could read the series of an earlier one.
var _ = BeforeEach(func() {
	RequestsTotal.Reset()
})
