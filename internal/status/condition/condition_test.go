package condition

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Conditions", func() {
	Describe("SetAnomaly", func() {
		PIt("adds the condition True with its reason and message while the anomaly is active", func() {})
		PIt("removes the condition once the anomaly ends", func() {})
	})

	Describe("Readiness", func() {
		PIt("sets Ready True with reason IsReady when nothing is wrong", func() {})
		PIt("sets Ready False with the reason and message it is given", func() {})
		PIt("emits a Warning ResourceNotReady carrying the reason when Ready flips to False", func() {})
		PIt("emits a Normal ResourceReady when Ready flips back to True", func() {})
		PIt("emits a ResourceNotReady when a resource with no Ready condition yet is not ready", func() {})
		PIt("emits nothing when a resource with no Ready condition yet is ready", func() {})
		PIt("emits nothing when the persisted Ready is already False, as after a restart", func() {})
		PIt("emits nothing while Ready keeps its status", func() {})
		PIt("exports kuik_resource_not_ready at 1 with the reason while Ready is False", func() {})
		PIt("deletes the kuik_resource_not_ready series once Ready is True", func() {})
		PIt("replaces the series when the reason changes, never keeping two", func() {})
		PIt("deletes the series of a forgotten resource", func() {})
	})
})
