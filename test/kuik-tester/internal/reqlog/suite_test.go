package reqlog

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestReqlog(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reqlog Suite")
}
