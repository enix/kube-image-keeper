package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// Notes is the release-notes.yaml file the release-notes skill writes: the single source of
// a release's notes. The release body and the chart's artifacthub.io/changes are rendered
// from it.
type Notes struct {
	// Version is the version semantic-release computes, without the "v" of its tag.
	Version string `json:"version"`
	// Intro is an optional paragraph above the sections.
	Intro      string      `json:"intro,omitempty"`
	Highlights []Highlight `json:"highlights"`
	Breaking   []Breaking  `json:"breaking,omitempty"`
	Limits     []Limit     `json:"limits,omitempty"`
	Features   []Change    `json:"features,omitempty"`
	Fixes      []Change    `json:"fixes,omitempty"`
}

// Highlight is a bullet of the Highlights section.
type Highlight struct {
	Lead string `json:"lead"`
	Text string `json:"text"`
}

// Breaking is a bullet of the Breaking changes section, for the readers of From or earlier.
type Breaking struct {
	// From is the previous major line ("v2") or the last version that does not have the
	// change ("3.0.0-alpha.5").
	From string `json:"from"`
	Text string `json:"text"`
	Refs Refs   `json:"refs"`
}

// Limit is a bullet of the Known limits section.
type Limit struct {
	Area string `json:"area"`
	Lead string `json:"lead"`
	Text string `json:"text"`
	Refs Refs   `json:"refs,omitempty"`
}

// Change is a bullet of the Features or Fixes list of the Changelog.
type Change struct {
	Area string `json:"area"`
	Text string `json:"text"`
	Refs Refs   `json:"refs"`
}

// Refs is the work behind a bullet.
type Refs struct {
	// Pulls are the pull requests of this repository that delivered the change.
	Pulls []int `json:"pulls,omitempty"`
	// Commits are the commits of this repository, only for a change without a pull request.
	Commits []string `json:"commits,omitempty"`
	// Closes are the issues of this repository the change closes.
	Closes []int `json:"closes,omitempty"`
	// External are issues of other repositories, as "owner/repo#N".
	External []string `json:"external,omitempty"`
}

func (r Refs) empty() bool {
	return len(r.Pulls) == 0 && len(r.Commits) == 0 && len(r.Closes) == 0 && len(r.External) == 0
}

// areas are the areas of the Changelog and Known limits, in the order users meet them. The
// entries of a list follow this order, so the file reads as the rendered notes.
var areas = []string{
	"Install",
	"Routing",
	"Pull secrets",
	"Mirroring",
	"Monitoring",
	"Registries",
	"Configuration",
	"Observability",
	"Chart",
}

var (
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	majorPattern    = regexp.MustCompile(`^v[0-9]+$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	externalPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[1-9][0-9]*$`)
	linkPattern     = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

// parse decodes a release-notes.yaml strictly (an unknown or duplicate field is an error) and
// validates it.
func parse(data []byte) (*Notes, error) {
	var n Notes
	if err := yaml.UnmarshalStrict(data, &n); err != nil {
		return nil, err
	}
	if problems := n.validate(); len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return &n, nil
}

// checkVersion refuses notes written for another version than want, the version the Release
// workflow computes; an empty want checks nothing.
func checkVersion(n *Notes, want string) error {
	if want != "" && n.Version != want {
		return fmt.Errorf("the notes are for %s, not for %s, the version to release", n.Version, want)
	}
	return nil
}

func (n *Notes) validate() []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !versionPattern.MatchString(n.Version) {
		add("version: %q is not a version such as 3.0.0-beta.1", n.Version)
	}
	if n.Intro != "" {
		checkText(n.Intro, false, func(p string) { add("intro: %s", p) })
	}
	if len(n.Highlights) < 3 || len(n.Highlights) > 6 {
		add("highlights: %d bullets, want 3 to 6", len(n.Highlights))
	}
	for i, h := range n.Highlights {
		at := func(p string) { add("highlights[%d]: %s", i, p) }
		checkLead(h.Lead, at)
		checkText(h.Text, false, at)
	}
	for i, b := range n.Breaking {
		at := func(p string) { add("breaking[%d]: %s", i, p) }
		if !majorPattern.MatchString(b.From) && !versionPattern.MatchString(b.From) {
			at(fmt.Sprintf("from %q is neither a major line such as v2 nor a version such as 3.0.0-alpha.5", b.From))
		}
		checkText(b.Text, true, at)
		checkRefs(b.Refs, true, at)
	}
	checkAreas("limits", len(n.Limits), func(i int) string { return n.Limits[i].Area }, add)
	for i, l := range n.Limits {
		at := func(p string) { add("limits[%d]: %s", i, p) }
		checkLead(l.Lead, at)
		checkText(l.Text, true, at)
		checkRefs(l.Refs, false, at)
	}
	for name, list := range map[string][]Change{"features": n.Features, "fixes": n.Fixes} {
		checkAreas(name, len(list), func(i int) string { return list[i].Area }, add)
		for i, c := range list {
			at := func(p string) { add("%s[%d]: %s", name, i, p) }
			checkText(c.Text, false, at)
			checkRefs(c.Refs, true, at)
		}
	}
	slices.Sort(problems)
	return problems
}

// checkAreas reports an unknown area, and a list whose entries do not follow the order of
// areas.
func checkAreas(name string, count int, area func(int) string, add func(string, ...any)) {
	last := -1
	for i := range count {
		rank := slices.Index(areas, area(i))
		switch {
		case rank < 0:
			add("%s[%d]: unknown area %q, want one of %s", name, i, area(i), strings.Join(areas, ", "))
		case rank < last:
			add("%s[%d]: area %q comes after %q, want the order %s", name, i, area(i), areas[last], strings.Join(areas, ", "))
		default:
			last = rank
		}
	}
}

func checkLead(lead string, at func(string)) {
	if strings.TrimSpace(lead) == "" {
		at("lead is empty")
		return
	}
	checkText(lead, false, at)
}

// checkText reports an empty text, one with surrounding spaces or several lines, an emoji,
// and a Markdown link where links are not allowed.
func checkText(text string, links bool, at func(string)) {
	switch {
	case strings.TrimSpace(text) == "":
		at("text is empty")
		return
	case strings.TrimSpace(text) != text:
		at(fmt.Sprintf("%q has leading or trailing spaces", text))
	case strings.Contains(text, "\n"):
		at(fmt.Sprintf("%q spans several lines", text))
	}
	if hasEmoji(text) {
		at(fmt.Sprintf("%q holds an emoji: only the section headings carry one", text))
	}
	if !links && linkPattern.MatchString(text) {
		at(fmt.Sprintf("%q holds a Markdown link: only breaking and limits may link", text))
	}
}

// checkRefs reports a malformed reference, a commit next to a pull request, and a missing
// pull request or commit where the work must be named.
func checkRefs(r Refs, required bool, at func(string)) {
	for _, p := range r.Pulls {
		if p <= 0 {
			at(fmt.Sprintf("pull request %d is not a number above 0", p))
		}
	}
	for _, c := range r.Closes {
		if c <= 0 {
			at(fmt.Sprintf("issue %d is not a number above 0", c))
		}
	}
	for _, c := range r.Commits {
		if !commitPattern.MatchString(c) {
			at(fmt.Sprintf("commit %q is not 7 to 40 lowercase hex digits", c))
		}
	}
	for _, e := range r.External {
		if !externalPattern.MatchString(e) {
			at(fmt.Sprintf("external issue %q is not owner/repo#N", e))
		}
	}
	if len(r.Pulls) > 0 && len(r.Commits) > 0 {
		at("refs name commits next to pull requests: a commit only for a change without a pull request")
	}
	if required && len(r.Pulls) == 0 && len(r.Commits) == 0 {
		at("refs name no pull request nor commit")
	}
}

// hasEmoji reports a rune of the emoji blocks, or the variation selector that turns a symbol
// into one.
func hasEmoji(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x1F000 && r <= 0x1FAFF,
			r >= 0x2600 && r <= 0x27BF,
			r >= 0x2B00 && r <= 0x2BFF,
			r == 0xFE0F, r == 0x200D:
			return true
		}
	}
	return false
}
