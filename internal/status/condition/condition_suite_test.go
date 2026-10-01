package condition

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCondition(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Condition Suite")
}
