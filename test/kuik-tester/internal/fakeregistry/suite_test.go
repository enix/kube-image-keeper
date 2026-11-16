package fakeregistry

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ctx = context.Background()

func TestFakeRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FakeRegistry Suite")
}
