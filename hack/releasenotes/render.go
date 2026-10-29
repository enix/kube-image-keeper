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

// groupAbove is the number of Known limits above which they are grouped under their areas.
const groupAbove = 6

// render returns the release body: the shape CONTRIBUTING.md ("Release notes") describes.
func render(n *Notes) string {
	var b strings.Builder
	if n.Intro != "" {
		b.WriteString(n.Intro + "\n\n")
	}
	if len(n.Breaking) == 0 {
		b.WriteString(noBreaking + "\n\n")
	}

	b.WriteString("## ✨ Highlights\n\n")
	for _, h := range n.Highlights {
		fmt.Fprintf(&b, "- **%s**: %s\n", h.Lead, h.Text)
	}

	if len(n.Breaking) > 0 {
		b.WriteString("\n## ⚠️ Breaking changes\n\n")
		for _, c := range n.Breaking {
			fmt.Fprintf(&b, "- **%s**: %s%s\n", from(c.From), c.Text, refs(c.Refs))
		}
	}

	if len(n.Limits) > 0 || n.LimitsUnchangedSince != "" {
		b.WriteString("\n## 🚧 Known limits\n\n")
		bullets := make([]areaBullet, len(n.Limits))
		for i, l := range n.Limits {
			bullets[i] = areaBullet{area: l.Area, line: fmt.Sprintf("- **%s**: %s%s\n", l.Lead, l.Text, refs(l.Refs))}
		}
		if len(bullets) > groupAbove {
			b.WriteString(grouped(bullets))
		} else {
			for _, x := range bullets {
				b.WriteString(x.line)
			}
		}
		if since := n.LimitsUnchangedSince; since != "" {
			// A paragraph, outside the areas: a blank line ends the list above it.
			if len(bullets) > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "The other known limits are unchanged since [%s](%s/releases/tag/v%s).\n", since, repository, since)
		}
	}

	b.WriteString("\n## 📋 Changelog\n\n### Features\n\n")
	b.WriteString(changelog(n.Features, "new feature", "new features"))
	b.WriteString("\n### Fixes\n\n")
	b.WriteString(changelog(n.Fixes, "fix", "fixes"))
	return b.String()
}

// from is the bold lead of a breaking change.
func from(f string) string {
	if majorPattern.MatchString(f) {
		return "From " + f
	}
	return "From `" + f + "` or earlier"
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

// grouped writes the bullets under a bold line per area; validation keeps them in area order.
func grouped(bullets []areaBullet) string {
	var b strings.Builder
	for i, x := range bullets {
		if i == 0 || x.area != bullets[i-1].area {
			if i > 0 {
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
