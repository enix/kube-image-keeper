// Command testoutline prints the Ginkgo specs of the repository as a plain-English tree, or
// the test cases added and removed since a git ref, so that the cases can be reviewed before
// their bodies are written. It parses the sources with Ginkgo's own outline package and runs
// nothing.
package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2/ginkgo/outline"
)

// node is one entry of a Ginkgo outline: a container (Describe, Context, When,
// DescribeTable) or a spec (It, Specify, Entry). Setup nodes are dropped when parsing.
type node struct {
	Name    string  `json:"name"`
	Text    string  `json:"text"`
	Spec    bool    `json:"spec"`
	Focused bool    `json:"focused"`
	Pending bool    `json:"pending"`
	Nodes   []*node `json:"nodes"`
}

// kept are the node kinds that describe behaviour, once their F, P or X prefix is removed.
var kept = map[string]bool{
	"Describe": true, "Context": true, "When": true, "DescribeTable": true, "DescribeTableSubtree": true,
	"It": true, "Specify": true, "Entry": true,
}

// parse returns the outline of one Ginkgo test source, or nil for a file that imports no
// Ginkgo (a plain Go test, a helper).
func parse(filename string, src []byte) ([]*node, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}
	o, err := outline.FromASTFile(fset, file)
	if err != nil {
		return nil, nil // not a Ginkgo file
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("encoding the outline of %s: %w", filename, err)
	}
	var nodes []*node
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("decoding the outline of %s: %w", filename, err)
	}
	return prune(nodes), nil
}

// prune drops the setup nodes (BeforeEach, By, DeferCleanup...) and keeps the tree of
// containers and specs.
func prune(nodes []*node) []*node {
	var out []*node
	for _, n := range nodes {
		if !kept[strings.TrimLeft(n.Name, "FPX")] {
			continue
		}
		n.Nodes = prune(n.Nodes)
		out = append(out, n)
	}
	return out
}

// label is how a node reads in the tree: its text, or its kind when the text is not a
// literal (an Entry built in a loop, whose text Ginkgo reports as "undefined").
func (n *node) label() string {
	text := n.Text
	if text == "" || text == "undefined" {
		text = n.Name + " (dynamic text)"
	}
	switch {
	case n.Pending:
		return text + " [pending]"
	case n.Focused:
		return text + " [focused]"
	}
	return text
}

// writeTree prints the outline indented by depth, specs as bullets.
func writeTree(w io.Writer, nodes []*node, depth int) {
	for _, n := range nodes {
		bullet := ""
		if n.Spec {
			bullet = "- "
		}
		_, _ = fmt.Fprintf(w, "%s%s%s\n", strings.Repeat("  ", depth), bullet, n.label())
		writeTree(w, n.Nodes, depth+1)
	}
}

// cases flattens the outline into one line per spec, its containers joined with " / ", in
// source order. This is the unit the diff compares.
func cases(nodes []*node) []string {
	var out []string
	var walk func(nodes []*node, path []string)
	walk = func(nodes []*node, path []string) {
		for _, n := range nodes {
			p := append(slices.Clone(path), n.label())
			if n.Spec {
				out = append(out, strings.Join(p, " / "))
			}
			walk(n.Nodes, p)
		}
	}
	walk(nodes, nil)
	return out
}

// diffCases returns the cases of newCases missing from oldCases (added) and the cases of
// oldCases missing from newCases (removed), each in its own source order. A renamed case is
// one removed and one added.
func diffCases(oldCases, newCases []string) (added, removed []string) {
	old := make(map[string]int, len(oldCases))
	for _, c := range oldCases {
		old[c]++
	}
	cur := make(map[string]int, len(newCases))
	for _, c := range newCases {
		cur[c]++
	}
	for _, c := range newCases {
		if old[c] == 0 {
			added = append(added, c)
		} else {
			old[c]--
		}
	}
	for _, c := range oldCases {
		if cur[c] == 0 {
			removed = append(removed, c)
		} else {
			cur[c]--
		}
	}
	return added, removed
}
