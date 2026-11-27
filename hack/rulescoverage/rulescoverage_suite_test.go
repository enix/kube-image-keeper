package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRulesCoverage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rules Coverage Suite")
}
