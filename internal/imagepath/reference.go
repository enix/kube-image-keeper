// Package imagepath parses image references and repository paths, and matches references
// against `repository` / `repositoryGroup` entries on a trie of path segments. See
// docs/v3/spec.md, "Alternatives matching".
package imagepath

import "errors"

// errNotImplemented is returned by the stubs until the package is implemented.
var errNotImplemented = errors.New("not implemented")

// Reference is a normalised image reference: `nginx:1.27` is host `docker.io`, segments
// `[library nginx]`, tag `1.27`.
type Reference struct {
	// Host is the registry host, with its port when it has one.
	Host string
	// Segments are the path segments below the host.
	Segments []string
	// Tag is the tag, empty when the reference carries only a digest.
	Tag string
	// Digest is the digest, empty when the reference carries none.
	Digest string
}

// Parse normalises an image reference.
func Parse(s string) (Reference, error) {
	return Reference{}, errNotImplemented
}

// String renders the canonical form of the reference.
func (r Reference) String() string {
	return ""
}

// Repository renders the host and the path, without tag or digest.
func (r Reference) Repository() string {
	return ""
}

// Kind is the form of an entry: a single repository, or every repository under a path.
type Kind int

const (
	// Repository matches that exact repository only.
	Repository Kind = iota
	// Group matches every repository located under the path, whatever its depth.
	Group
)

// Path is a normalised `repository` or `repositoryGroup` value: a host and path segments, no
// tag and no digest.
type Path struct {
	// Host is the registry host, with its port when it has one.
	Host string
	// Segments are the path segments below the host.
	Segments []string
}

// ParsePath parses and normalises a `repository` or `repositoryGroup` value.
func ParsePath(s string, kind Kind) (Path, error) {
	return Path{}, errNotImplemented
}
