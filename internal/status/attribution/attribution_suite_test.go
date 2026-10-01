package attribution

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAttribution(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Attribution Suite")
}
