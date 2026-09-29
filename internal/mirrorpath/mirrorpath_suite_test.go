package mirrorpath

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMirrorPath(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MirrorPath Suite")
}
