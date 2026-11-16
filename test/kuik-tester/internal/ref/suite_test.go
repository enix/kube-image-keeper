package ref

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRef(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ref Suite")
}
