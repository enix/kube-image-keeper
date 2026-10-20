package clusterid

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterID(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ClusterID Suite")
}
