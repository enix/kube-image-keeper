package main

import (
	"fmt"
	"strconv"
	"strings"
)

// none is the line of a Changelog list with nothing to list.
const none = "None in this release.\n"

// noBreaking follows the introduction of notes without breaking changes, as its own bold
// paragraph: their section is left out, and a reader upgrading looks for this answer first.
const noBreaking = "**No breaking change in this release.**"

// render returns the release body: the shape CONTRIBUTING.md ("Release notes") describes.
func render(n *Notes) string {
	var b strings.Builder
	if n.Intro != "" {
		b.WriteString(n.Intro + "\n\n")
	}
	if len(n.Breaking) == 0 {
		b.WriteString(noBreaking + "\n\n")
	}

	// Each section ends with a line break; sep is the blank line before the next one, none
	// at the start of the body.
	sep := ""
	if len(n.Highlights) > 0 {
		b.WriteString("## ✨ Highlights\n\n")
		for _, h := range n.Highlights {
			fmt.Fprintf(&b, "- **%s**: %s\n", h.Lead, h.Text)
		}
		sep = "\n"
	}

	if len(n.Breaking) > 0 {
		b.WriteString(sep + "## ⚠️ Breaking changes\n\n")
		main, previous := n.breakingByReader()
		if len(previous) == 0 {
			// A single reader: the bullets need no lead.
			b.WriteString(breakingList(n.Breaking))
		} else {
			// Something breaks the version directly before: each reader finds what breaks the
			// version it runs under its own heading, the main reader first. A change that breaks
			// both is written once per reader. When nothing breaks the main reader, its heading
			// says so rather than leave it to guess from a missing one.
			fmt.Fprintf(&b, "### From %s\n\n", n.mainReader())
			if len(main) == 0 {
				fmt.Fprintf(&b, "Nothing breaks since %s.\n", n.Since)
			} else {
				b.WriteString(breakingList(main))
			}
			fmt.Fprintf(&b, "\n### From %s\n\n%s", n.Previous.Version, breakingList(previous))
		}
		sep = "\n"
	}

	// The known limits live in docs/known-limits.md, read in its latest state: the notes only
	// say what changes, in the entries above and below. The Changelog makes one comparison,
	// from since, which it always names; the changes since the version directly before are not
	// repeated here: Artifact Hub and the release's CHANGELOG.md asset carry them.
	b.WriteString(sep + "## 📋 Changelog\n\n")
	fmt.Fprintf(&b, "Changes since [%s](%s/releases/tag/v%s).\n\n", n.Since, repository, n.Since)
	b.WriteString("### Features\n\n")
	b.WriteString(changelog(n.Features, "new feature", "new features"))
	b.WriteString("\n### Fixes\n\n")
	b.WriteString(changelog(n.Fixes, "fix", "fixes"))
	return b.String()
}

// breakingList is the bullets of a list of breaking changes.
func breakingList(list []Breaking) string {
	var b strings.Builder
	for _, c := range list {
		fmt.Fprintf(&b, "- %s%s\n", c.Text, refs(c.Refs))
	}
	return b.String()
}

// refs is the trailing parenthesis of a bullet: pull requests, commits, closed issues, then
// issues of other repositories, which GitHub links by themselves in a release body.
func refs(r Refs) string {
	if r.empty() {
		return ""
	}
	var parts []string
	for _, p := range r.Pulls {
		parts = append(parts, "#"+strconv.Itoa(p))
	}
	parts = append(parts, r.Commits...)
	for _, c := range r.Closes {
		parts = append(parts, "closes #"+strconv.Itoa(c))
	}
	parts = append(parts, r.External...)
	return " (" + strings.Join(parts, ", ") + ")"
}

type areaBullet struct {
	area string
	line string
}

// grouped writes the bullets of a list by area; validation keeps them in area order. An area of
// a single entry gets no bold line of its own: those entries come first, in one list, each led by
// its area. Each area of several entries follows under its bold line, in area order.
func grouped(bullets []areaBullet) string {
	count := map[string]int{}
	for _, x := range bullets {
		count[x.area]++
	}
	var b strings.Builder
	for _, x := range bullets {
		if count[x.area] == 1 {
			fmt.Fprintf(&b, "- **%s**: %s", x.area, strings.TrimPrefix(x.line, "- "))
		}
	}
	for i, x := range bullets {
		if count[x.area] == 1 {
			continue
		}
		if i == 0 || x.area != bullets[i-1].area {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "**%s**\n\n", x.area)
		}
		b.WriteString(x.line)
	}
	return b.String()
}

// changelog is a collapsed list of the Changelog, grouped by area, or the line of an empty one.
func changelog(changes []Change, one, many string) string {
	if len(changes) == 0 {
		return none
	}
	noun := many
	if len(changes) == 1 {
		noun = one
	}
	bullets := make([]areaBullet, len(changes))
	for i, c := range changes {
		bullets[i] = areaBullet{area: c.Area, line: fmt.Sprintf("- %s%s\n", c.Text, refs(c.Refs))}
	}
	return fmt.Sprintf("<details><summary>%d %s</summary>\n\n%s\n</details>\n", len(changes), noun, grouped(bullets))
}
