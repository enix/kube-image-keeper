package main

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Notes is the release-notes.yaml file the release-notes skill writes: the single source of
// a release's notes. The release body and the chart's artifacthub.io/changes are rendered
// from it.
type Notes struct {
	// Version is the version semantic-release computes, without the "v" of its tag.
	Version string `json:"version"`
	// Since is the version the changes are counted from: the baseline of the readers of
	// these notes.
	Since string `json:"since"`
	// Intro is an optional paragraph above the sections.
	Intro      string      `json:"intro,omitempty"`
	Highlights []Highlight `json:"highlights"`
	Breaking   []Breaking  `json:"breaking,omitempty"`
	Features   []Change    `json:"features,omitempty"`
	Fixes      []Change    `json:"fixes,omitempty"`
	// Previous is, in the first release of a stability level only, the changes since the
	// version directly before it, for the readers who run every version.
	Previous *Previous `json:"previous,omitempty"`
}

// Previous is the changes since the version directly before, whatever its stability level.
type Previous struct {
	Version  string   `json:"version"`
	Features []Change `json:"features,omitempty"`
	Fixes    []Change `json:"fixes,omitempty"`
}

// Highlight is a bullet of the Highlights section.
type Highlight struct {
	Lead string `json:"lead"`
	Text string `json:"text"`
}

// Breaking is a bullet of the Breaking changes section, for the reader of From.
type Breaking struct {
	// From is the reader the change breaks for: since, its major line ("v2") when since is
	// on an earlier major, or the version of Previous. With Previous, the section groups the
	// changes by reader; a change that breaks both readers is written once per reader.
	From string `json:"from"`
	Text string `json:"text"`
	Refs Refs   `json:"refs"`
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

// areas are the areas of the Changelog, in the order users meet them. The
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
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z]+\.[0-9]+)?$`)
	firstPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z]+\.1)?$`)
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
	switch {
	case n.Since == "":
		add("since: missing, want the version the changes are counted from")
	case !versionPattern.MatchString(n.Since):
		add("since: %q is not a version such as 3.0.0-beta.1", n.Since)
	case versionPattern.MatchString(n.Version) && compareVersions(n.Since, n.Version) >= 0:
		add("since: %s is not below %s", n.Since, n.Version)
	}
	if n.Intro != "" {
		checkText(n.Intro, false, func(p string) { add("intro: %s", p) })
	}
	if len(n.Highlights) > 5 {
		add("highlights: %d bullets, want 0 to 5", len(n.Highlights))
	}
	for i, h := range n.Highlights {
		at := func(p string) { add("highlights[%d]: %s", i, p) }
		checkLead(h.Lead, at)
		checkText(h.Text, false, at)
	}
	readers := n.readers()
	for i, b := range n.Breaking {
		at := func(p string) { add("breaking[%d]: %s", i, p) }
		switch {
		case !majorPattern.MatchString(b.From) && !versionPattern.MatchString(b.From):
			at(fmt.Sprintf("from %q is neither a major line such as v2 nor a version such as 3.0.0-alpha.5", b.From))
		case len(readers) > 0 && !slices.Contains(readers, b.From):
			at(fmt.Sprintf("from %s serves no reader of these notes, want %s", b.From, oneOf(readers)))
		}
		checkText(b.Text, true, at)
		checkRefs(b.Refs, at)
	}
	checkChanges("features", n.Features, add)
	checkChanges("fixes", n.Fixes, add)
	if n.Previous != nil {
		n.Previous.validate(n, add)
	}
	slices.Sort(problems)
	return problems
}

// readers are the values a breaking change's from may take, one per reader the notes serve:
// since, its major line when since is on an earlier major, and the version directly before
// when the notes have one. A change that breaks only intermediate pre-releases concerns none
// of them. Without a valid since, there is nothing to check against.
func (n *Notes) readers() []string {
	if !versionPattern.MatchString(n.Since) {
		return nil
	}
	readers := []string{n.Since}
	if versionPattern.MatchString(n.Version) && major(n.Since) < major(n.Version) {
		readers = append(readers, "v"+strconv.Itoa(major(n.Since)))
	}
	if n.Previous != nil && versionPattern.MatchString(n.Previous.Version) {
		readers = append(readers, n.Previous.Version)
	}
	return readers
}

// breakingByReader splits the breaking changes between the main reader, coming from since,
// and the reader coming from the version directly before.
func (n *Notes) breakingByReader() (main, previous []Breaking) {
	for _, b := range n.Breaking {
		if n.Previous != nil && b.From == n.Previous.Version {
			previous = append(previous, b)
		} else {
			main = append(main, b)
		}
	}
	return main, previous
}

// mainReader names the reader coming from since: its major line when since is on an earlier
// major, since itself otherwise.
func (n *Notes) mainReader() string {
	if major(n.Since) < major(n.Version) {
		return "v" + strconv.Itoa(major(n.Since))
	}
	return n.Since
}

// major is the major number of a version of versionPattern.
func major(v string) int {
	m, _, _ := strings.Cut(v, ".")
	n, _ := strconv.Atoi(m)
	return n
}

// oneOf lists values as "a", "a or b", "a, b or c".
func oneOf(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}

// validate checks the changes since the version directly before: only the first release of a
// stability level has them, and that version lies between since and the notes' own.
func (p *Previous) validate(n *Notes, add func(string, ...any)) {
	if versionPattern.MatchString(n.Version) && !firstPattern.MatchString(n.Version) {
		add("previous: %s is not the first release of a stability level, "+
			"a version X.Y.Z or a pre-release numbered 1", n.Version)
	}
	switch {
	case p.Version == "":
		add("previous.version: missing, want the version directly before %s", n.Version)
	case !versionPattern.MatchString(p.Version):
		add("previous.version: %q is not a version such as 3.0.0-alpha.9", p.Version)
	case versionPattern.MatchString(n.Version) && compareVersions(p.Version, n.Version) >= 0:
		add("previous.version: %s is not below %s", p.Version, n.Version)
	case versionPattern.MatchString(n.Since) && compareVersions(p.Version, n.Since) <= 0:
		add("previous.version: %s is not above %s", p.Version, n.Since)
	}
	checkChanges("previous.features", p.Features, add)
	checkChanges("previous.fixes", p.Fixes, add)
}

// checkChanges reports the problems of a Features or Fixes list.
func checkChanges(name string, list []Change, add func(string, ...any)) {
	checkAreas(name, len(list), func(i int) string { return list[i].Area }, add)
	for i, c := range list {
		at := func(p string) { add("%s[%d]: %s", name, i, p) }
		checkText(c.Text, false, at)
		checkRefs(c.Refs, at)
	}
}

// compareVersions orders two versions of versionPattern by semver precedence: X.Y.Z as
// numbers, a pre-release below its release, then the channel and the number of the
// pre-release.
func compareVersions(a, b string) int {
	key := func(v string) (nums [4]int, channel string) {
		core, pre, found := strings.Cut(v, "-")
		for i, part := range strings.Split(core, ".") {
			nums[i], _ = strconv.Atoi(part)
		}
		if !found {
			nums[3] = math.MaxInt
			return nums, ""
		}
		channel, number, _ := strings.Cut(pre, ".")
		nums[3], _ = strconv.Atoi(number)
		return nums, channel
	}
	an, ac := key(a)
	bn, bc := key(b)
	if c := slices.Compare(an[:3], bn[:3]); c != 0 {
		return c
	}
	if ac != bc && ac != "" && bc != "" {
		return strings.Compare(ac, bc)
	}
	return cmp.Compare(an[3], bn[3])
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
		at(fmt.Sprintf("%q holds a Markdown link: only breaking changes may link", text))
	}
}

// checkRefs reports a malformed reference, a commit next to a pull request, and a missing
// pull request or commit.
func checkRefs(r Refs, at func(string)) {
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
	if len(r.Pulls) == 0 && len(r.Commits) == 0 {
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
