package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestReleaseNotes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Release Notes Suite")
}
