package imagepath

import "slices"

// Trie maps repository paths to values, one node per path segment under the host. A lookup
// walks the segments of the reference once, whatever the number of values.
type Trie[V any] struct {
	hosts map[string]*node[V]
}

type node[V any] struct {
	children     map[string]*node[V]
	repositories []V
	groups       []V
}

// Match is a value whose path matched a reference.
type Match[V any] struct {
	// Value is the value inserted with the path.
	Value V
	// Kind is the form the path was inserted with.
	Kind Kind
	// Depth is the number of path segments of the matched path.
	Depth int
	// Remainder holds the segments of the reference below a group; empty for a repository.
	Remainder []string
}

// NewTrie returns an empty trie.
func NewTrie[V any]() *Trie[V] {
	return &Trie[V]{hosts: map[string]*node[V]{}}
}

// Insert adds a value under a path. A path holds any number of values of each kind.
func (t *Trie[V]) Insert(p Path, kind Kind, v V) {
	n := t.hosts[p.Host]
	if n == nil {
		n = &node[V]{}
		t.hosts[p.Host] = n
	}
	for _, segment := range p.Segments {
		child := n.children[segment]
		if child == nil {
			if n.children == nil {
				n.children = map[string]*node[V]{}
			}
			child = &node[V]{}
			n.children[segment] = child
		}
		n = child
	}
	if kind == Repository {
		n.repositories = append(n.repositories, v)
	} else {
		n.groups = append(n.groups, v)
	}
}

// Match returns the values whose path matches the reference, most specific first: the
// repositories, then the groups from the deepest to the shallowest. A repository matches
// the reference's exact path; a group matches a reference strictly below it.
func (t *Trie[V]) Match(ref Reference) []Match[V] {
	n := t.hosts[ref.Host]
	var groups []Match[V]
	for depth := 0; n != nil; depth++ {
		if depth == len(ref.Segments) {
			matches := make([]Match[V], 0, len(n.repositories)+len(groups))
			for _, v := range n.repositories {
				matches = append(matches, Match[V]{Value: v, Kind: Repository, Depth: depth})
			}
			return append(matches, groups...)
		}
		remainder := slices.Clone(ref.Segments[depth:])
		// Deeper groups go first: prepend this depth's groups to the ones found above.
		found := make([]Match[V], 0, len(n.groups))
		for _, v := range n.groups {
			found = append(found, Match[V]{Value: v, Kind: Group, Depth: depth, Remainder: remainder})
		}
		groups = append(found, groups...)
		n = n.children[ref.Segments[depth]]
	}
	return groups
}

// MostSpecificPerOwner keeps the most specific match of each owner, in the order of
// matches. Within one owner, specificity picks the entry, hence the remainder carried over;
// across owners every one keeps its match.
func MostSpecificPerOwner[V any, K comparable](matches []Match[V], owner func(V) K) []Match[V] {
	seen := map[K]bool{}
	kept := make([]Match[V], 0, len(matches))
	for _, m := range matches {
		k := owner(m.Value)
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, m)
	}
	return kept
}

// Rewrite renders the reference a match rewrites to under target: the target path, the
// remainder below a group, and the original tag or digest.
func Rewrite[V any](ref Reference, m Match[V], target Path) Reference {
	return Reference{
		Host:     target.Host,
		Segments: slices.Concat(target.Segments, m.Remainder),
		Tag:      ref.Tag,
		Digest:   ref.Digest,
	}
}
