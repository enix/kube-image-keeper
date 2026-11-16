package dest

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ctx is the context of every spec that needs no bound of its own.
var ctx = context.Background()

func TestDest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Dest Suite")
}
