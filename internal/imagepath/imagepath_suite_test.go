package imagepath

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImagePath(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ImagePath Suite")
}
