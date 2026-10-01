package capped

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCapped(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Capped Suite")
}
