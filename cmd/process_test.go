package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/sets"
)

var _ = Describe("Process", func() {
	Describe("reading the subcommand", func() {
		DescribeTable("starts the process the subcommand names",
			func(name string) {
				p, err := parseProcess(name)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.name).To(Equal(name))
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
			Entry("refuses a process that would run all three at once", "all"),
		)
	})

	Describe("responsibilities", func() {
		It("gives each process exactly one, so what a pod is allowed to do follows from what it runs", func() {
			for _, p := range processes {
				responsibilities := 0
				for _, runs := range []bool{p.servesWebhook, p.runsReconcilers, p.runsSecretSyncer} {
					if runs {
						responsibilities++
					}
				}
				Expect(responsibilities).To(Equal(1), "process %q", p.name)
			}
		})

		It("covers the three responsibilities, so nothing is left unrun by a complete deployment", func() {
			var webhook, reconcilers, syncer int
			for _, p := range processes {
				if p.servesWebhook {
					webhook++
				}
				if p.runsReconcilers {
					reconcilers++
				}
				if p.runsSecretSyncer {
					syncer++
				}
			}
			Expect([]int{webhook, reconcilers, syncer}).To(HaveEach(1))
		})
	})

	Describe("leader election", func() {
		It("never elects the webhook, so every replica answers admission and losing one changes nothing", func() {
			p, err := parseProcess("webhook")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.usesLeaderElection()).To(BeFalse())
			Expect(p.leaderElectionID).To(BeEmpty())
		})

		DescribeTable("elects every process that writes",
			func(name string) {
				p, err := parseProcess(name)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.usesLeaderElection()).To(BeTrue())
			},
			Entry("elects the reconciler", "reconciler"),
			Entry("elects the secret syncer", "secret-syncer"),
		)

		It("gives each elected process a lease of its own, so none can make another stand down", func() {
			var ids []string
			for _, p := range processes {
				if p.usesLeaderElection() {
					ids = append(ids, p.leaderElectionID)
				}
			}
			Expect(ids).To(HaveEach(HaveSuffix(".kuik.enix.io")))
			Expect(sets.New(ids...)).To(HaveLen(len(ids)))
		})
	})

	Describe("the usage", func() {
		It("names every process, so a call without a subcommand says what to run", func() {
			text := usage("manager")
			for _, p := range processes {
				Expect(text).To(ContainSubstring("manager " + p.name))
				Expect(text).To(ContainSubstring(p.summary))
			}
		})
	})
})
