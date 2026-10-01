package routingstatus

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRoutingStatus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RoutingStatus Suite")
}
