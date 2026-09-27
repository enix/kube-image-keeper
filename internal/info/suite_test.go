package info

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInfo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Info Suite")
}
