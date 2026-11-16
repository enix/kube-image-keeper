package testerconfig

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTesterConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "TesterConfig Suite")
}
