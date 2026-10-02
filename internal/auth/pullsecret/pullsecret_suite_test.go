package pullsecret

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPullSecret(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pull Secret Suite")
}
