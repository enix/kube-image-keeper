package capped

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Bounded lists", func() {
	Describe("Cap", func() {
		PIt("keeps a list under the cap whole", func() {})
		PIt("keeps the oldest entries by since once a list exceeds the cap", func() {})
		PIt("breaks ties on since by key, so the kept entries do not churn between writes", func() {})
	})

	Describe("the truncated map", func() {
		PIt("is absent when no list reached the cap", func() {})
		PIt("records, per list, how many entries were left out", func() {})
		PIt("drops a list that came back under the cap", func() {})
	})

	Describe("ListCapacityPressure", func() {
		PIt("is absent while every list stays under 80% of the cap", func() {})
		PIt("is True with reason ListNearCapacity from 80% of the cap", func() {})
		PIt("is True with reason ListTruncated once a list leaves entries out", func() {})
		PIt("is removed once every list is back under 80% of the cap", func() {})
	})

	Describe("the kuik_status_list_* series", func() {
		PIt("sets kuik_status_list_entries to the entries written, per list", func() {})
		PIt("counts an entry in kuik_status_list_dropped_total the first time it is left out", func() {})
		PIt("does not count again an entry still left out at the next write", func() {})
		PIt("counts again an entry that came back and is left out anew", func() {})
		PIt("keeps the counts of two resources of different kinds sharing a name apart", func() {})
		PIt("removes the series of a forgotten resource", func() {})
	})
})
