package synth

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSynth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Synth Suite")
}
