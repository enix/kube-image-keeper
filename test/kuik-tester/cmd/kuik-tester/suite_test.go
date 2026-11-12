package main

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ctx is the root context of the specs.
var ctx = context.Background()

func TestKuikTester(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kuik Tester Suite")
}
