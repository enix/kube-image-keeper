package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Process", func() {
	Describe("reading the subcommand", func() {
		DescribeTable("starts the process the subcommand names",
			func(name string) {
				p, err := parseProcess(name)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.name).To(Equal(processName(name)))
			},
			Entry("starts the webhook", "webhook"),
			Entry("starts the reconciler", "reconciler"),
			Entry("starts the secret syncer", "secret-syncer"),
		)

		DescribeTable("refuses anything else, naming the accepted subcommands so the operator can fix the call",
			func(name string) {
				_, err := parseProcess(name)
				Expect(err).To(MatchError(ContainSubstring("webhook, reconciler, secret-syncer")))
			},
			Entry("refuses an unknown process", "mirror"),
			Entry("refuses an empty subcommand", ""),
			Entry("refuses a subcommand differing only by case", "Webhook"),
		)
	})

	Describe("the usage", func() {
		It("names every process, so a call without a subcommand says what to run", func() {
			text := usage("manager")
			for _, p := range processes {
				Expect(text).To(ContainSubstring("manager " + string(p.name)))
				Expect(text).To(ContainSubstring(p.summary))
			}
		})
	})
})
