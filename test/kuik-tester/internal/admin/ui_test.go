package admin

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

var _ = Describe("UI pages", func() {
	DescribeTable("serves the page on a fresh state",
		func(path string) {
			uiExpectPage(newUIEnv().get(path))
		},
		Entry("images", "/"),
		Entry("rules", "/rules"),
		Entry("settings", "/settings"),
		Entry("destinations", "/destinations"),
		Entry("requests", requestsPath),
		Entry("state", "/state"),
	)

	It("answers 404 to an unknown path", func() {
		Expect(newUIEnv().get("/nope").Code).To(Equal(http.StatusNotFound))
	})

	It("serves the embedded static files", func() {
		want, err := uiFS.ReadFile("ui/static/htmx.min.js")
		Expect(err).NotTo(HaveOccurred())
		Expect(want).NotTo(BeEmpty())
		rec := newUIEnv().get("/static/htmx.min.js")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal(string(want)))
	})

	It("escapes the user names", func() {
		e := newUIEnv()
		Expect(e.svc.PutUser(api.User{Name: `<script>x</script>`, Password: "p"})).To(Succeed())
		Expect(e.get("/settings").Body.String()).NotTo(ContainSubstring("<script>x"))
	})
})
