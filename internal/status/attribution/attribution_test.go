package attribution

import (
	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Attribution", func() {
	Describe("Containers", func() {
		DescribeTable("the state of a container",
			func(state State) {},
			PEntry("is untouched when no annotation names it", Untouched),
			PEntry("is rewritten when rewrites names it and its live image equals rewrittenTo", Rewritten),
			PEntry("is conceded when conceded-rewrites names it", Conceded),
			PEntry("is stale when rewrites names it and its live image differs from rewrittenTo", Stale),
			PEntry("is noAlternatives when no-alternatives names it", NoAlternatives),
		)
		DescribeTable("the origin of a container",
			func(state State) {},
			PEntry("is the recorded origin of a rewritten container", Rewritten),
			PEntry("is the live image of an untouched container", Untouched),
			PEntry("is the live image of a noAlternatives container", NoAlternatives),
			PEntry("is the live image of a conceded container, the other webhook's reference", Conceded),
			PEntry("is the live image of a stale container", Stale),
		)
		PIt("normalises the live image, so a short name reads as its docker.io form", func() {})
		PIt("returns the initContainers along with the containers", func() {})
		PIt("never returns an ephemeralContainer", func() {})
		PIt("reads a container named only by a malformed annotation as untouched, the other annotations still read", func() {})
		PIt("compares the live image to rewrittenTo as written, without normalising either", func() {})
	})

	Describe("Container.StateFor", func() {
		PIt("keeps the state of a rewritten, conceded or stale container for the resource of its record", func() {})
		PIt("reads a rewritten, conceded or stale container as untouched for any other resource", func() {})
		PIt("keeps noAlternatives for every resource that offered a candidate", func() {})
		PIt("reads a noAlternatives container as untouched for a resource that did not offer one", func() {})
	})

	Describe("Static", func() {
		PIt("recognises a mirror pod by its kubernetes.io/config.mirror annotation", func() {})
		PIt("reads a pod without the annotation as routable", func() {})
	})

	DescribeTable("Live",
		func(phase corev1.PodPhase, live bool) {},
		PEntry("counts a Pending pod", corev1.PodPending, true),
		PEntry("counts a Running pod", corev1.PodRunning, true),
		PEntry("leaves out a Succeeded pod", corev1.PodSucceeded, false),
		PEntry("leaves out a Failed pod", corev1.PodFailed, false),
		PEntry("leaves out a pod in Unknown phase", corev1.PodUnknown, false),
	)
})
