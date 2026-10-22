package main

import (
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// repository is where the pull requests, commits and issues of Refs live.
const repository = "https://github.com/enix/kube-image-keeper"

// change is an entry of the chart's artifacthub.io/changes annotation, in Artifact Hub's
// structured form.
type change struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Links       []link `json:"links,omitempty"`
}

type link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// changes returns the artifacthub.io/changes YAML list: the breaking changes (changed), the
// features (added) and the fixes (fixed). Artifact Hub shows descriptions as plain text, so a
// Markdown link is reduced to its text. It returns nothing when there is no entry.
func changes(n *Notes) ([]byte, error) {
	list := make([]change, 0, len(n.Breaking)+len(n.Features)+len(n.Fixes))
	for _, b := range n.Breaking {
		list = append(list, entry("changed", from(b.From)+": "+b.Text, b.Refs))
	}
	for _, f := range n.Features {
		list = append(list, entry("added", f.Area+": "+f.Text, f.Refs))
	}
	for _, f := range n.Fixes {
		list = append(list, entry("fixed", f.Area+": "+f.Text, f.Refs))
	}
	if len(list) == 0 {
		return nil, nil
	}
	return yaml.Marshal(list)
}

func entry(kind, description string, r Refs) change {
	c := change{Kind: kind, Description: linkPattern.ReplaceAllString(description, "$1")}
	for _, p := range r.Pulls {
		c.Links = append(c.Links, link{Name: "#" + strconv.Itoa(p), URL: repository + "/pull/" + strconv.Itoa(p)})
	}
	for _, sha := range r.Commits {
		c.Links = append(c.Links, link{Name: sha, URL: repository + "/commit/" + sha})
	}
	for _, i := range r.Closes {
		c.Links = append(c.Links, link{Name: "#" + strconv.Itoa(i), URL: repository + "/issues/" + strconv.Itoa(i)})
	}
	for _, e := range r.External {
		repo, number, _ := strings.Cut(e, "#")
		c.Links = append(c.Links, link{Name: e, URL: "https://github.com/" + repo + "/issues/" + number})
	}
	return c
}
