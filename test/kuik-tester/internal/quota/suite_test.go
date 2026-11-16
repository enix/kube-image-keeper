package quota

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQuota(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Quota Suite")
}
