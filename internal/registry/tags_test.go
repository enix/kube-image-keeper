package registry

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("ListTags", func() {
	PIt("lists every tag of the repository, other clusters' included", func() {})
	PIt("returns no tag for a repository the registry does not know", func() {})
	PIt("lists every page of a paginated tags/list before returning", func() {})
})

var _ = Describe("DeleteTag", func() {
	PIt("deletes the tag and leaves the other tags of the same manifest", func() {})
	PIt("refuses a digest reference", func() {})
	PIt("succeeds when the tag is already gone", func() {})
	PIt("returns ErrDeleteUnsupported when the registry answers 405", func() {})
	PIt("counts nothing in kuik_registry_requests_total", func() {})
})
