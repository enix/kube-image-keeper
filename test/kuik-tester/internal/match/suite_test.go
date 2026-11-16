package match

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The package declares Label, which a dot-import of ginkgo would shadow: ginkgo is
// imported by name here and in the specs.
func TestMatch(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Match Suite")
}
