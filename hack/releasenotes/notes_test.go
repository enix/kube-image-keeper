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
since: 3.0.0-beta.1
highlights:
  - lead: One
    text: the first highlight.
  - lead: Two
    text: the second highlight.
  - lead: Three
    text: the third highlight.
breaking:
  - from: 3.0.0-beta.1
    text: The value ` + "`a`" + ` is renamed ` + "`b`" + `.
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
	breaking    = "breaking:\n  - from: 3.0.0-beta.1\n    text: The value `a` is renamed `b`.\n" + breakingRef
	features    = "features:\n  - area: Routing\n    " + featureText + "\n    " + featureRefs + "\n" +
		"  - area: Mirroring\n    text: A mirroring feature.\n    refs: {pulls: [803]}\n"
	fixes = "fixes:\n  - area: Chart\n    text: A chart fix.\n    refs: {pulls: [804]}\n"
	repo  = "https://github.com/enix/kube-image-keeper"
)

const fourthToSixth = `
  - lead: Four
    text: the fourth highlight.
  - lead: Five
    text: the fifth highlight.
  - lead: Six
    text: the sixth highlight.`

// The changes since the version directly before, which withPrevious appends to valid.
const (
	previousFeatures = "  features:\n" +
		"    - area: Install\n      text: An install feature since the alpha.\n      refs: {pulls: [811]}\n" +
		"    - area: Monitoring\n      text: A monitoring feature since the alpha.\n      refs: {pulls: [812]}\n"
	previousFixes = "  fixes:\n" +
		"    - area: Registries\n      text: A registries fix since the alpha.\n      refs: {pulls: [813]}\n"
	previousBlock = "previous:\n  version: 3.0.0-alpha.9\n" + previousFeatures + previousFixes
)

// withPrevious returns valid turned into the notes of the first beta, counted from 2.3.1,
// with its breaking change from v2 and the changes since 3.0.0-alpha.9, then applies the
// replacements of with.
func withPrevious(pairs ...string) []byte {
	GinkgoHelper()
	return with(append([]string{
		"version: 3.0.0-beta.2", "version: 3.0.0-beta.1",
		"since: 3.0.0-beta.1", "since: 2.3.1",
		"from: 3.0.0-beta.1", "from: v2",
		fixes, fixes + previousBlock,
	}, pairs...)...)
}

// previousBreaking is a breaking change for the reader of the version directly before, which
// the specs add to withPrevious.
const previousBreaking = "  - from: 3.0.0-alpha.9\n    text: The value `c` is removed.\n    refs: {pulls: [805]}\n"

// Entries the changelog specs add to valid: a second Routing feature, and 2 Chart fixes that
// replace its own.
const (
	secondRouting = "  - area: Routing\n    text: Another routing feature.\n    refs: {pulls: [805]}\n"
	twoChartFixes = "  - area: Chart\n    text: A chart fix.\n    refs: {pulls: [806]}\n" +
		"  - area: Chart\n    text: Another chart fix.\n    refs: {pulls: [807]}\n"
)

// noHighlights removes the highlights of valid.
var noHighlights = []string{"highlights:\n  - lead: One\n    text: the first highlight.\n" +
	"  - lead: Two\n    text: the second highlight.\n  - lead: Three\n    text: the third highlight.\n", ""}

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

	DescribeTable("accepts a breaking change for a reader the notes serve",
		func(data []byte) {
			Expect(parse(data)).NotTo(BeNil())
		},
		Entry("the version the changes are counted from",
			withPrevious("from: v2", "from: 2.3.1")),
		Entry("the major line of the version the changes are counted from, on an earlier major",
			withPrevious()),
		Entry("the version directly before",
			withPrevious("from: v2", "from: 3.0.0-alpha.9")),
	)

	DescribeTable("accepts changes counted from an earlier version",
		func(since string) {
			Expect(parse(with("since: 3.0.0-beta.1", "since: "+since, "from: 3.0.0-beta.1", "from: "+since))).NotTo(BeNil())
		},
		Entry("the previous major line", "2.3.1"),
		Entry("an earlier pre-release channel", "3.0.0-alpha.9"),
		Entry("an earlier minor", "2.9.10"),
	)

	DescribeTable("accepts the changes since the version directly before in the first release of a stability level",
		func(version, previous string) {
			data := withPrevious("version: 3.0.0-beta.1", "version: "+version, "version: 3.0.0-alpha.9", "version: "+previous)
			Expect(parse(data)).NotTo(BeNil())
		},
		Entry("the first beta, after an alpha", "3.0.0-beta.1", "3.0.0-alpha.9"),
		Entry("the first release candidate, after a beta", "3.0.0-rc.1", "3.0.0-beta.4"),
		Entry("a stable release, after a release candidate", "3.0.0", "3.0.0-rc.2"),
	)

	It("accepts notes without highlights", func() {
		Expect(parse(with(noHighlights...))).NotTo(BeNil())
	})

	It("accepts a Markdown link in a breaking change", func() {
		data := with("is renamed `b`.", "is renamed `b`, see [the docs](https://kuik.enix.io/).")
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
		Entry("more than 5 highlights",
			with("    text: the third highlight.", "    text: the third highlight."+fourthToSixth),
			"6 bullets, want 0 to 5"),
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
			with(featureText, "text: A routing [feature](https://kuik.enix.io/)."), "only breaking changes may link"),
		Entry("a Markdown link in a highlight",
			with(firstText, "text: the first [highlight](https://kuik.enix.io/)."), "only breaking changes may link"),
		Entry("a Markdown link in the intro",
			with("highlights:", "intro: see [the docs](https://kuik.enix.io/).\nhighlights:"),
			"only breaking changes may link"),
		Entry("a breaking change from a malformed version",
			with("from: 3.0.0-beta.1", `from: "3.0"`), "is neither a major line"),
		Entry("a breaking change from a pre-release between since and the version directly before",
			withPrevious("from: v2", "from: 3.0.0-alpha.3"),
			"breaking[0]: from 3.0.0-alpha.3 serves no reader of these notes, want 2.3.1, v2 or 3.0.0-alpha.9"),
		Entry("a breaking change from a major line when the changes are counted from the same major",
			with("from: 3.0.0-beta.1", "from: v3"),
			"breaking[0]: from v3 serves no reader of these notes, want 3.0.0-beta.1"),
		Entry("a breaking change from another major line than the one of the version the changes are counted from",
			withPrevious("from: v2", "from: v1"),
			"breaking[0]: from v1 serves no reader of these notes, want 2.3.1, v2 or 3.0.0-alpha.9"),
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
		Entry("no version the changes are counted from",
			with("since: 3.0.0-beta.1\n", ""), "since: missing, want the version the changes are counted from"),
		Entry("changes counted from a malformed version",
			with("since: 3.0.0-beta.1", "since: v3.0.0-beta.1"), `since: "v3.0.0-beta.1" is not a version`),
		Entry("changes counted from the version of the notes themselves",
			with("since: 3.0.0-beta.1", "since: 3.0.0-beta.2"), "since: 3.0.0-beta.2 is not below 3.0.0-beta.2"),
		Entry("changes counted from a later pre-release, its number compared as a number",
			with("since: 3.0.0-beta.1", "since: 3.0.0-beta.10"), "since: 3.0.0-beta.10 is not below 3.0.0-beta.2"),
		Entry("changes counted from the release of the same version",
			with("since: 3.0.0-beta.1", "since: 3.0.0"), "since: 3.0.0 is not below 3.0.0-beta.2"),
		Entry("changes counted from a later patch",
			with("since: 3.0.0-beta.1", "since: 3.0.1-alpha.1"), "since: 3.0.1-alpha.1 is not below 3.0.0-beta.2"),
		Entry("known limits, which live in docs/known-limits.md",
			with("features:", "limits:\n  - area: Install\n    lead: First\n    text: a limit.\nfeatures:"),
			`unknown field "limits"`),
		Entry("the release whose known limits still hold, which docs/known-limits.md replaces",
			with("highlights:", "limitsUnchangedSince: 3.0.0-beta.1\nhighlights:"),
			`unknown field "limitsUnchangedSince"`),
		Entry("changes since the version directly before in a pre-release numbered above 1",
			withPrevious("version: 3.0.0-beta.1", "version: 3.0.0-beta.2"),
			"previous: 3.0.0-beta.2 is not the first release of a stability level, a version X.Y.Z or a pre-release numbered 1"),
		Entry("changes since a version directly before left out",
			withPrevious("  version: 3.0.0-alpha.9\n", ""),
			"previous.version: missing, want the version directly before 3.0.0-beta.1"),
		Entry("changes since a malformed version directly before",
			withPrevious("version: 3.0.0-alpha.9", "version: v3.0.0-alpha.9"),
			`previous.version: "v3.0.0-alpha.9" is not a version`),
		Entry("changes since a version directly before that is not below the notes' version",
			withPrevious("version: 3.0.0-alpha.9", "version: 3.0.0-beta.1"),
			"previous.version: 3.0.0-beta.1 is not below 3.0.0-beta.1"),
		Entry("changes since a version directly before that is not above the version the changes are counted from",
			withPrevious("version: 3.0.0-alpha.9", "version: 2.3.1"),
			"previous.version: 2.3.1 is not above 2.3.1"),
		Entry("an unknown area among the features since the version directly before",
			withPrevious("area: Monitoring", "area: Webhook"),
			`previous.features[1]: unknown area "Webhook"`),
		Entry("areas out of order among the features since the version directly before",
			withPrevious("area: Install", "area: Observability"),
			`previous.features[1]: area "Monitoring" comes after "Observability"`),
		Entry("a Markdown link in a fix since the version directly before",
			withPrevious("text: A registries fix since the alpha.", "text: A registries [fix](https://kuik.enix.io/)."),
			`previous.fixes[0]: "A registries [fix](https://kuik.enix.io/)." holds a Markdown link`),
		Entry("a fix since the version directly before with neither pull request nor commit",
			withPrevious("refs: {pulls: [813]}", "refs: {closes: [750]}"),
			"previous.fixes[0]: refs name no pull request nor commit"),
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

	It("leaves the Highlights section out when there is none", func() {
		Expect(render(mustParse(with(append(noHighlights, "version:", "intro: An introduction.\nversion:")...)))).To(
			HavePrefix("An introduction.\n\n## ⚠️ Breaking changes\n\n- The value"))
		Expect(render(mustParse(with(append(noHighlights, breaking, "")...)))).To(
			HavePrefix("**No breaking change in this release.**\n\n## 📋 Changelog\n"))
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
		Expect(body).To(ContainSubstring("## ⚠️ Breaking changes\n\n- The value"))
		Expect(body).NotTo(ContainSubstring("No breaking change"))
	})

	It("goes from the Breaking changes to the Changelog, with no Known limits section", func() {
		body := render(mustParse([]byte(valid)))
		Expect(body).To(ContainSubstring("is renamed `b`. (#801)\n\n## 📋 Changelog\n"))
		Expect(body).NotTo(ContainSubstring("Known limits"))
	})

	DescribeTable("always opens the Changelog with the version the changes are counted from",
		func(data []byte, since string) {
			Expect(render(mustParse(data))).To(ContainSubstring("## 📋 Changelog\n\n" +
				"Changes since [" + since + "](" + repo + "/releases/tag/v" + since + ").\n\n### Features\n"))
		},
		Entry("a later pre-release counted from the previous one of its level", []byte(valid), "3.0.0-beta.1"),
		Entry("a stable counted from the previous stable of its line",
			with("version: 3.0.0-beta.2", "version: 3.0.2", "since: 3.0.0-beta.1", "since: 3.0.1",
				"from: 3.0.0-beta.1", "from: 3.0.1"), "3.0.1"),
		Entry("the first release of a level, next to the version directly before", withPrevious(), "2.3.1"),
	)

	It("collapses the changelog lists, leading each entry with its area when every area has one, "+
		"counting one entry in the singular", func() {
		Expect(render(mustParse([]byte(valid)))).To(HaveSuffix(`## 📋 Changelog

Changes since [3.0.0-beta.1](https://github.com/enix/kube-image-keeper/releases/tag/v3.0.0-beta.1).

### Features

<details><summary>2 new features</summary>

- **Routing**: A routing feature. (#802)
- **Mirroring**: A mirroring feature. (#803)

</details>

### Fixes

<details><summary>1 fix</summary>

- **Chart**: A chart fix. (#804)

</details>
`))
	})

	It("lists the areas of a single entry first, led by their area, then each area of several entries under its bold line",
		func() {
			body := render(mustParse(with(
				"  - area: Mirroring\n", secondRouting+"  - area: Mirroring\n",
				"fixes:\n", "fixes:\n"+twoChartFixes, "  - area: Chart\n    text: A chart fix.\n    refs: {pulls: [804]}\n", "",
			)))
			Expect(body).To(ContainSubstring("<details><summary>3 new features</summary>\n\n" +
				"- **Mirroring**: A mirroring feature. (#803)\n\n" +
				"**Routing**\n\n- A routing feature. (#802)\n- Another routing feature. (#805)\n\n</details>"))
			Expect(body).To(ContainSubstring("<details><summary>2 fixes</summary>\n\n" +
				"**Chart**\n\n- A chart fix. (#806)\n- Another chart fix. (#807)\n\n</details>"))
		})

	It("keeps the area order among the areas of a single entry and among the areas of several", func() {
		body := render(mustParse(with(
			"  - area: Mirroring\n", secondRouting+"  - area: Mirroring\n",
			"fixes:\n  - area: Chart", "fixes:\n  - area: Install\n    text: An install fix.\n    refs: {pulls: [808]}\n"+
				"  - area: Monitoring\n    text: A monitoring fix.\n    refs: {pulls: [809]}\n"+
				"  - area: Monitoring\n    text: Another monitoring fix.\n    refs: {pulls: [810]}\n  - area: Chart",
			"    refs: {pulls: [804]}\n",
			"    refs: {pulls: [804]}\n  - area: Chart\n    text: Another chart fix.\n    refs: {pulls: [807]}\n",
		)))
		Expect(body).To(ContainSubstring("<details><summary>5 fixes</summary>\n\n" +
			"- **Install**: An install fix. (#808)\n\n" +
			"**Monitoring**\n\n- A monitoring fix. (#809)\n- Another monitoring fix. (#810)\n\n" +
			"**Chart**\n\n- A chart fix. (#804)\n- Another chart fix. (#807)\n\n</details>"))
	})

	It("leaves the changes since the version directly before out of the body", func() {
		body := render(mustParse(withPrevious()))
		Expect(body).To(HaveSuffix("### Fixes\n\n<details><summary>1 fix</summary>\n\n" +
			"- **Chart**: A chart fix. (#804)\n\n</details>\n"))
		Expect(body).NotTo(ContainSubstring("3.0.0-alpha.9"))
		Expect(body).NotTo(ContainSubstring("since the alpha"))
	})

	It("lists the breaking changes as plain bullets when the notes serve a single reader", func() {
		body := render(mustParse([]byte(valid)))
		Expect(body).To(ContainSubstring(
			"## ⚠️ Breaking changes\n\n- The value `a` is renamed `b`. (#801)\n\n## 📋 Changelog"))
		Expect(body).NotTo(ContainSubstring("From"))
	})

	It("lists plain breaking bullets when nothing breaks the reader of the version directly before", func() {
		body := render(mustParse(withPrevious()))
		Expect(body).To(ContainSubstring(
			"## ⚠️ Breaking changes\n\n- The value `a` is renamed `b`. (#801)\n\n## 📋 Changelog"))
		Expect(body).NotTo(ContainSubstring("### From"))
		Expect(body).NotTo(ContainSubstring("Nothing breaks"))
	})

	It("groups the breaking changes under the reader they break for, the main reader first", func() {
		body := render(mustParse(withPrevious("breaking:\n", "breaking:\n"+previousBreaking)))
		Expect(body).To(ContainSubstring("## ⚠️ Breaking changes\n\n" +
			"### From v2\n\n- The value `a` is renamed `b`. (#801)\n\n" +
			"### From 3.0.0-alpha.9\n\n- The value `c` is removed. (#805)\n\n## 📋 Changelog"))
		Expect(body).NotTo(ContainSubstring("or earlier"))
	})

	It("shows a change that breaks both readers in both groups", func() {
		both := strings.Replace(previousBreaking, "The value `c` is removed.", "The value `a` is renamed `b`.", 1)
		body := render(mustParse(withPrevious(breakingRef, breakingRef+both)))
		Expect(body).To(ContainSubstring("### From v2\n\n- The value `a` is renamed `b`. (#801)\n\n" +
			"### From 3.0.0-alpha.9\n\n- The value `a` is renamed `b`. (#805)\n"))
	})

	It("names the main reader by the version the changes are counted from when it is on the same major", func() {
		body := render(mustParse(withPrevious(
			"version: 3.0.0-beta.1", "version: 3.1.0",
			"since: 2.3.1", "since: 3.0.0",
			"from: v2", "from: 3.0.0",
			"version: 3.0.0-alpha.9", "version: 3.1.0-rc.2",
			breakingRef, breakingRef+"  - from: 3.1.0-rc.2\n    text: The value `c` is removed.\n    refs: {pulls: [805]}\n",
		)))
		Expect(body).To(ContainSubstring("## ⚠️ Breaking changes\n\n### From 3.0.0\n\n- The value `a`"))
	})

	It("says under its heading that nothing breaks the main reader when only the second one has breaking changes", func() {
		Expect(render(mustParse(withPrevious("from: v2", "from: 3.0.0-alpha.9")))).To(ContainSubstring(
			"## ⚠️ Breaking changes\n\n### From v2\n\nNothing breaks since 2.3.1.\n\n" +
				"### From 3.0.0-alpha.9\n\n- The value `a` is renamed `b`. (#801)\n\n## 📋 Changelog"))
	})

	It("says no breaking change in bold, with no section, when nothing breaks for either reader", func() {
		breakingFromV2 := strings.Replace(breaking, "from: 3.0.0-beta.1", "from: v2", 1)
		body := render(mustParse(withPrevious(breakingFromV2, "")))
		Expect(body).To(HavePrefix("**No breaking change in this release.**\n\n## ✨ Highlights\n"))
		Expect(body).NotTo(ContainSubstring("Breaking changes"))
		Expect(body).NotTo(ContainSubstring("Nothing breaks"))
	})

	DescribeTable("ends a bullet with its references",
		func(refs, rendered string) {
			body := render(mustParse(with(featureRefs, "refs: "+refs)))
			Expect(body).To(ContainSubstring("- **Routing**: A routing feature. " + rendered + "\n"))
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
	// split returns the descriptions of the breaking changes, and the other entries as
	// "<kind> <description>".
	split := func(data []byte) (breaking, others []string) {
		GinkgoHelper()
		for _, c := range decode(data) {
			if c.Kind == "changed" {
				breaking = append(breaking, c.Description)
			} else {
				others = append(others, c.Kind+" "+c.Description)
			}
		}
		return breaking, others
	}

	It("lists the breaking changes, the features and the fixes, each feature and fix under its area", func() {
		list := decode([]byte(valid))
		Expect(list).To(HaveLen(4))
		Expect(list[0]).To(HaveField("Kind", "changed"))
		Expect(list[0]).To(HaveField("Description", "The value `a` is renamed `b`."))
		Expect(list[1]).To(HaveField("Kind", "added"))
		Expect(list[1]).To(HaveField("Description", "Routing: A routing feature."))
		Expect(list[2]).To(HaveField("Description", "Mirroring: A mirroring feature."))
		Expect(list[3]).To(HaveField("Kind", "fixed"))
		Expect(list[3]).To(HaveField("Description", "Chart: A chart fix."))
	})

	It("reduces a Markdown link to its text", func() {
		list := decode(with("is renamed `b`.", "is renamed `b`, see [the docs](https://kuik.enix.io/)."))
		Expect(list[0].Description).To(Equal("The value `a` is renamed `b`, see the docs."))
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

	DescribeTable("lists the changes since the version directly before instead of the main ones, "+
		"breaking changes included",
		func(data []byte) {
			changed, lines := split(data)
			Expect(changed).To(Equal([]string{"The value `c` is removed."}))
			Expect(lines).To(Equal([]string{
				"added Install: An install feature since the alpha.",
				"added Monitoring: A monitoring feature since the alpha.",
				"fixed Registries: A registries fix since the alpha.",
			}))
		},
		Entry("in a pre-release", withPrevious(breakingRef, breakingRef+previousBreaking)),
		Entry("in a stable release", withPrevious("version: 3.0.0-beta.1", "version: 3.0.0",
			breakingRef, breakingRef+previousBreaking)),
	)

	It("prints nothing when nothing changed since the version directly before, whatever the main lists hold", func() {
		breakingFromV2 := strings.Replace(breaking, "from: 3.0.0-beta.1", "from: v2", 1)
		out, err := changes(mustParse(withPrevious(breakingFromV2, "", previousFeatures, "", previousFixes, "")))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})

	It("prints nothing when there is no breaking change, feature or fix", func() {
		out, err := changes(mustParse(with(breaking, "", fixes, "", features, "")))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeEmpty())
	})
})
