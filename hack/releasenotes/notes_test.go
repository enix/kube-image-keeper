package main

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

// valid is a release-notes.yaml that passes every rule. The specs derive their fixtures from
// it by replacement.
const valid = `version: 3.0.0-beta.2
highlights:
  - lead: One
    text: the first highlight.
  - lead: Two
    text: the second highlight.
  - lead: Three
    text: the third highlight.
breaking:
  - from: 3.0.0-beta.1
    text: the value ` + "`a`" + ` is renamed ` + "`b`" + `.
    refs: {pulls: [801]}
features:
  - area: Routing
    text: A routing feature.
    refs: {pulls: [802]}
  - area: Mirroring
    text: A mirroring feature.
    refs: {pulls: [803]}
fixes:
  - area: Chart
    text: A chart fix.
    refs: {pulls: [804]}
`

// Pieces of valid the specs replace.
const (
	firstText   = "text: the first highlight."
	featureText = "text: A routing feature."
	featureRefs = "refs: {pulls: [802]}"
	breakingRef = "    refs: {pulls: [801]}\n"
	breaking    = "breaking:\n  - from: 3.0.0-beta.1\n    text: the value `a` is renamed `b`.\n" + breakingRef
	features    = "features:\n  - area: Routing\n    " + featureText + "\n    " + featureRefs + "\n" +
		"  - area: Mirroring\n    text: A mirroring feature.\n    refs: {pulls: [803]}\n"
	fixes = "fixes:\n  - area: Chart\n    text: A chart fix.\n    refs: {pulls: [804]}\n"
	repo  = "https://github.com/enix/kube-image-keeper"
)

const fourthToSeventh = `
  - lead: Four
    text: the fourth highlight.
  - lead: Five
    text: the fifth highlight.
  - lead: Six
    text: the sixth highlight.
  - lead: Seven
    text: the seventh highlight.`

const twoLimits = `limits:
  - area: Install
    lead: First
    text: a limit.
  - area: Mirroring
    lead: Second
    text: another limit, see [the docs](https://kuik.enix.io/).
features:`

const sevenLimits = `limits:
  - area: Install
    lead: L1
    text: a limit.
  - area: Install
    lead: L2
    text: a limit.
  - area: Routing
    lead: L3
    text: a limit.
  - area: Routing
    lead: L4
    text: a limit.
  - area: Mirroring
    lead: L5
    text: a limit.
  - area: Mirroring
    lead: L6
    text: a limit.
  - area: Chart
    lead: L7
    text: a limit.
features:`

// unchangedSince sets limitsUnchangedSince to 3.0.0-beta.1, and unchangedLine is the line it
// renders.
const (
	unchangedSince = "limitsUnchangedSince: 3.0.0-beta.1\nhighlights:"
	unchangedLine  = "The other known limits are unchanged since " +
		"[3.0.0-beta.1](" + repo + "/releases/tag/v3.0.0-beta.1).\n"
)

// with returns the fixture with exact replacements applied (old, new, old, new...), and fails
// the spec when a text to replace is absent, so a stale fixture cannot pass silently.
func with(pairs ...string) []byte {
	GinkgoHelper()
	data := valid
	for i := 0; i < len(pairs); i += 2 {
		Expect(data).To(ContainSubstring(pairs[i]))
		data = strings.Replace(data, pairs[i], pairs[i+1], 1)
	}
	return []byte(data)
}

func mustParse(data []byte) *Notes {
	GinkgoHelper()
	n, err := parse(data)
	Expect(err).NotTo(HaveOccurred())
	return n
}

func readFile(path string) []byte {
	GinkgoHelper()
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return data
}

var _ = Describe("Release notes file", func() {
	It("accepts a file that follows every rule", func() {
		Expect(parse([]byte(valid))).NotTo(BeNil())
	})

	It("accepts the notes of 3.0.0-beta.1", func() {
		Expect(parse(readFile("testdata/3.0.0-beta.1.yaml"))).NotTo(BeNil())
	})

	It("accepts a breaking change from the previous major line", func() {
		Expect(parse(with("from: 3.0.0-beta.1", "from: v2"))).NotTo(BeNil())
	})

	It("accepts the release whose other known limits still hold", func() {
		Expect(parse(with("highlights:", unchangedSince))).NotTo(BeNil())
	})

	It("accepts a Markdown link in a breaking change and in a known limit, and a limit without references", func() {
		data := with(
			"is renamed `b`.", "is renamed `b`, see [the docs](https://kuik.enix.io/).",
			"features:", twoLimits,
		)
		Expect(parse(data)).NotTo(BeNil())
	})

	DescribeTable("checks the version the notes are for against the version to release",
		func(want string, ok bool) {
			err := checkVersion(mustParse([]byte(valid)), want)
			if ok {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError("the notes are for 3.0.0-beta.2, not for " + want + ", the version to release"))
			}
		},
		Entry("the same version", "3.0.0-beta.2", true),
		Entry("another version", "3.0.0-beta.3", false),
		Entry("no version to check against", "", true),
	)

	DescribeTable("refuses a file that breaks a rule, naming the rule",
		func(data []byte, problem string) {
			_, err := parse(data)
			Expect(err).To(MatchError(ContainSubstring(problem)))
		},
		Entry("an unknown field",
			with("highlights:", "notes: more\nhighlights:"), "unknown field"),
		Entry("a field given twice",
			with("highlights:", "version: 3.0.0-beta.3\nhighlights:"), `"version" already set`),
		Entry("a version with its v",
			with("version: 3.0.0-beta.2", "version: v3.0.0-beta.2"), "is not a version"),
		Entry("fewer than 3 highlights",
			with("  - lead: Three\n    text: the third highlight.\n", ""), "2 bullets, want 3 to 6"),
		Entry("more than 6 highlights",
			with("    text: the third highlight.", "    text: the third highlight."+fourthToSeventh),
			"7 bullets, want 3 to 6"),
		Entry("an empty lead",
			with("lead: One", `lead: ""`), "lead is empty"),
		Entry("an empty text",
			with(firstText, `text: ""`), "text is empty"),
		Entry("a text with surrounding spaces",
			with(firstText, `text: " the first highlight."`), "leading or trailing spaces"),
		Entry("a text on several lines",
			with(firstText, `text: "the first\nhighlight."`), "spans several lines"),
		Entry("an emoji in a text",
			with(featureText, "text: A routing feature 🚀."), "holds an emoji"),
		Entry("an emoji in a lead",
			with("lead: One", "lead: One ✨"), "holds an emoji"),
		Entry("an emoji made by a variation selector",
			with("text: A chart fix.", "text: A chart fix ⚠️."), "holds an emoji"),
		Entry("a Markdown link in a feature",
			with(featureText, "text: A routing [feature](https://kuik.enix.io/)."), "only breaking and limits may link"),
		Entry("a Markdown link in a highlight",
			with(firstText, "text: the first [highlight](https://kuik.enix.io/)."), "only breaking and limits may link"),
		Entry("a Markdown link in the intro",
			with("highlights:", "intro: see [the docs](https://kuik.enix.io/).\nhighlights:"),
			"only breaking and limits may link"),
		Entry("a breaking change from a malformed version",
			with("from: 3.0.0-beta.1", `from: "3.0"`), "is neither a major line"),
		Entry("an unknown area",
			with("area: Routing", "area: Webhook"), `unknown area "Webhook"`),
		Entry("areas out of order",
			with("area: Routing", "area: Observability"), `area "Mirroring" comes after "Observability"`),
		Entry("a pull request number of 0",
			with(featureRefs, "refs: {pulls: [0]}"), "pull request 0 is not a number above 0"),
		Entry("an issue number of 0",
			with(featureRefs, "refs: {pulls: [802], closes: [0]}"), "issue 0 is not a number above 0"),
		Entry("a malformed commit",
			with(featureRefs, "refs: {commits: [ABC]}"), `commit "ABC" is not 7 to 40 lowercase hex digits`),
		Entry("a malformed external issue",
			with(featureRefs, "refs: {pulls: [802], external: [harbor#1]}"), `external issue "harbor#1" is not owner/repo#N`),
		Entry("a commit next to a pull request",
			with(featureRefs, "refs: {pulls: [802], commits: [5fa90de9]}"),
			"a commit only for a change without a pull request"),
		Entry("a feature with neither pull request nor commit",
			with(featureRefs, "refs: {closes: [750]}"), "refs name no pull request nor commit"),
		Entry("a breaking change without references",
			with(breakingRef, ""), "refs name no pull request nor commit"),
		Entry("unchanged limits since a malformed version",
			with("highlights:", "limitsUnchangedSince: v3.0.0-beta.1\nhighlights:"),
			`limitsUnchangedSince: "v3.0.0-beta.1" is not a version`),
		Entry("unchanged limits since a pre-release with an empty identifier",
			with("highlights:", "limitsUnchangedSince: 3.0.0-beta..1\nhighlights:"),
			`limitsUnchangedSince: "3.0.0-beta..1" is not a version`),
		Entry("unchanged limits since a pre-release without its number",
			with("highlights:", "limitsUnchangedSince: 3.0.0-beta\nhighlights:"),
			`limitsUnchangedSince: "3.0.0-beta" is not a version`),
		Entry("unchanged limits since the version of the notes themselves",
			with("highlights:", "limitsUnchangedSince: 3.0.0-beta.2\nhighlights:"),
			"limitsUnchangedSince: 3.0.0-beta.2 is the version of these notes"),
	)
})

var _ = Describe("Release body", func() {
	It("renders the notes of 3.0.0-beta.1 as they were validated", func() {
		body := render(mustParse(readFile("testdata/3.0.0-beta.1.yaml")))
		Expect(body).To(Equal(string(readFile("testdata/3.0.0-beta.1.md.golden"))))
	})

	It("starts with the Highlights when there is no introduction", func() {
		Expect(render(mustParse([]byte(valid)))).To(HavePrefix("## ✨ Highlights\n\n- **One**: the first highlight.\n"))
	})

	It("keeps the Fixes section with a line when it is empty", func() {
		Expect(render(mustParse(with(fixes, "fixes: []\n")))).To(HaveSuffix("### Fixes\n\nNone in this release.\n"))
	})

	It("leaves the Breaking changes section out when there is none, and says so in bold after the introduction", func() {
		body := render(mustParse(with(breaking, "", "highlights:", "intro: An introduction.\nhighlights:")))
		Expect(body).To(HavePrefix("An introduction.\n\n**No breaking change in this release.**\n\n## ✨ Highlights\n"))
		Expect(body).NotTo(ContainSubstring("Breaking changes"))
	})

	It("says there is no breaking change in bold first when the notes have no introduction", func() {
		Expect(render(mustParse(with(breaking, "")))).To(
			HavePrefix("**No breaking change in this release.**\n\n## ✨ Highlights\n"))
	})

	It("keeps the Breaking changes section and the introduction as written when there is a breaking change", func() {
		body := render(mustParse(with("highlights:", "intro: An introduction.\nhighlights:")))
		Expect(body).To(HavePrefix("An introduction.\n\n## ✨ Highlights\n"))
		Expect(body).To(ContainSubstring("## ⚠️ Breaking changes\n\n- **From `3.0.0-beta.1` or earlier**: the value"))
		Expect(body).NotTo(ContainSubstring("No breaking change"))
	})

	It("ends the Known limits with a line linking the release whose other limits still hold", func() {
		Expect(render(mustParse(with("highlights:", unchangedSince, "features:", twoLimits)))).To(ContainSubstring(
			"- **Second**: another limit, see [the docs](https://kuik.enix.io/).\n\n" + unchangedLine + "\n## 📋 Changelog"))
	})

	It("puts that line after the areas when the known limits are grouped", func() {
		Expect(render(mustParse(with("highlights:", unchangedSince, "features:", sevenLimits)))).To(ContainSubstring(
			"**Chart**\n\n- **L7**: a limit.\n\n" + unchangedLine + "\n## 📋 Changelog"))
	})

	It("holds only that line in the Known limits when no limit is new or changed", func() {
		Expect(render(mustParse(with("highlights:", unchangedSince)))).To(ContainSubstring(
			"## 🚧 Known limits\n\n" + unchangedLine + "\n## 📋 Changelog"))
	})

	It("leaves the Known limits section out when there is none", func() {
		Expect(render(mustParse([]byte(valid)))).NotTo(ContainSubstring("Known limits"))
	})

	It("lists up to 6 known limits without their areas", func() {
		Expect(render(mustParse(with("features:", twoLimits)))).To(ContainSubstring("## 🚧 Known limits\n\n" +
			"- **First**: a limit.\n" +
			"- **Second**: another limit, see [the docs](https://kuik.enix.io/).\n\n" +
			"## 📋 Changelog"))
	})

	It("collapses the changelog lists under their areas, counting one entry in the singular", func() {
		Expect(render(mustParse([]byte(valid)))).To(HaveSuffix(`## 📋 Changelog

### Features

<details><summary>2 new features</summary>

**Routing**

- A routing feature. (#802)

**Mirroring**

- A mirroring feature. (#803)

</details>

### Fixes

<details><summary>1 fix</summary>

**Chart**

- A chart fix. (#804)

</details>
`))
	})

	DescribeTable("leads a breaking change with the readers it concerns",
		func(from, lead string) {
			body := render(mustParse(with("from: 3.0.0-beta.1", "from: "+from)))
			Expect(body).To(ContainSubstring("- " + lead + ": the value"))
		},
		Entry("a version", "3.0.0-beta.1", "**From `3.0.0-beta.1` or earlier**"),
		Entry("the previous major line", "v2", "**From v2**"),
	)

	DescribeTable("ends a bullet with its references",
		func(refs, rendered string) {
			body := render(mustParse(with(featureRefs, "refs: "+refs)))
			Expect(body).To(ContainSubstring("- A routing feature. " + rendered + "\n"))
		},
		Entry("pull requests", "{pulls: [802, 805]}", "(#802, #805)"),
		Entry("commits", "{commits: [5fa90de9, 0a139c5d]}", "(5fa90de9, 0a139c5d)"),
		Entry("a closed issue after the pull request", "{pulls: [802], closes: [750]}", "(#802, closes #750)"),
		Entry("an issue of another repository last",
			"{pulls: [802], closes: [750], external: [goharbor/harbor#24213]}",
			"(#802, closes #750, goharbor/harbor#24213)"),
	)
})

var _ = Describe("Artifact Hub changes", func() {
	decode := func(data []byte) []change {
		GinkgoHelper()
		out, err := changes(mustParse(data))
		Expect(err).NotTo(HaveOccurred())
		var list []change
		Expect(yaml.UnmarshalStrict(out, &list)).To(Succeed())
		return list
	}

	It("lists the breaking changes, the features and the fixes, each feature and fix under its area", func() {
		list := decode([]byte(valid))
		Expect(list).To(HaveLen(4))
		Expect(list[0]).To(HaveField("Kind", "changed"))
		Expect(list[0]).To(HaveField("Description", "From `3.0.0-beta.1` or earlier: the value `a` is renamed `b`."))
		Expect(list[1]).To(HaveField("Kind", "added"))
		Expect(list[1]).To(HaveField("Description", "Routing: A routing feature."))
		Expect(list[2]).To(HaveField("Description", "Mirroring: A mirroring feature."))
		Expect(list[3]).To(HaveField("Kind", "fixed"))
		Expect(list[3]).To(HaveField("Description", "Chart: A chart fix."))
	})

	It("leaves the known limits out, the release whose other limits still hold included", func() {
		Expect(decode(with("highlights:", unchangedSince, "features:", twoLimits))).To(HaveLen(4))
	})

	It("reduces a Markdown link to its text", func() {
		list := decode(with("is renamed `b`.", "is renamed `b`, see [the docs](https://kuik.enix.io/)."))
		Expect(list[0].Description).To(Equal("From `3.0.0-beta.1` or earlier: the value `a` is renamed `b`, see the docs."))
	})

	DescribeTable("links each reference to its page",
		func(refs string, want link) {
			list := decode(with(featureRefs, "refs: "+refs))
			Expect(list[1].Links).To(ContainElement(want))
		},
		Entry("a pull request", "{pulls: [802]}",
			link{Name: "#802", URL: repo + "/pull/802"}),
		Entry("a commit", "{commits: [5fa90de9]}",
			link{Name: "5fa90de9", URL: repo + "/commit/5fa90de9"}),
		Entry("a closed issue", "{pulls: [802], closes: [750]}",
			link{Name: "#750", URL: repo + "/issues/750"}),
		Entry("an issue of another repository", "{pulls: [802], external: [goharbor/harbor#24213]}",
			link{Name: "goharbor/harbor#24213", URL: "https://github.com/goharbor/harbor/issues/24213"}),
	)

	It("prints nothing when there is no breaking change, feature or fix", func() {
		out, err := changes(mustParse(with(breaking, "", fixes, "", features, "")))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})
})
