package imagemetrics

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImageMetrics(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ImageMetrics Suite")
}
