package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSleep(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sleep Suite")
}
