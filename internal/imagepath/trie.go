package imagepath

// Trie maps repository paths to values, one node per path segment under the host.
type Trie[V any] struct{}

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
	return &Trie[V]{}
}

// Insert adds a value under a path. A path holds any number of values of each kind.
func (t *Trie[V]) Insert(p Path, kind Kind, v V) {}

// Match returns the values whose path matches the reference, most specific first.
func (t *Trie[V]) Match(ref Reference) []Match[V] {
	return nil
}

// MostSpecificPerOwner keeps the most specific match of each owner, in the order of matches.
func MostSpecificPerOwner[V any, K comparable](matches []Match[V], owner func(V) K) []Match[V] {
	return nil
}

// Rewrite renders the reference a match rewrites to under target: the target path, the
// remainder below a group, and the original tag or digest.
func Rewrite[V any](ref Reference, m Match[V], target Path) Reference {
	return Reference{}
}
