package pacing

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPacing(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pacing Suite")
}
