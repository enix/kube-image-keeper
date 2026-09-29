// Package imagepath parses image references and repository paths, and matches references
// against `repository` / `repositoryGroup` entries on a trie of path segments. See
// docs/v3/spec.md, "Alternatives matching".
package imagepath

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/distribution/reference"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
)

// Docker Hub, as normalised references name it, and the legacy alias it is also known by.
const (
	dockerHub        = "docker.io"
	legacyDockerHub  = "index.docker.io"
	dockerHubLibrary = "library"
)

var (
	// hostPattern is the host part of kuikv1alpha1.RepositoryPathPattern: it contains a dot,
	// carries a port, or is localhost.
	hostPattern       = regexp.MustCompile(`^(localhost(:[0-9]+)?|[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+(:[0-9]+)?|[a-zA-Z0-9-]+:[0-9]+)$`)
	groupPattern      = regexp.MustCompile(kuikv1alpha1.RepositoryPathPattern)
	repositoryPattern = regexp.MustCompile(kuikv1alpha1.RepositoryPattern)
)

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

// Parse normalises an image reference the way the container runtime does: a short name
// lands under docker.io/library, and a reference with neither tag nor digest is `latest`.
func Parse(s string) (Reference, error) {
	named, err := reference.ParseNormalizedNamed(lowerHost(s))
	if err != nil {
		return Reference{}, fmt.Errorf("parsing image reference %q: %w", s, err)
	}
	named = reference.TagNameOnly(named)

	ref := Reference{
		Host:     reference.Domain(named),
		Segments: strings.Split(reference.Path(named), "/"),
	}
	if tagged, ok := named.(reference.Tagged); ok {
		ref.Tag = tagged.Tag()
	}
	if digested, ok := named.(reference.Digested); ok {
		ref.Digest = digested.Digest().String()
	}
	return ref, nil
}

// lowerHost lowercases the registry host of a reference, recognised as distribution does:
// the first component when it contains a dot or a port, or is localhost. A DNS host is
// case-insensitive and the kubelet pulls `Quay.IO/acme/foo` from quay.io, so kuik compares
// hosts in lowercase, and does so before the Docker Hub rules apply.
func lowerHost(s string) string {
	host, rest, hasPath := strings.Cut(s, "/")
	if !hasPath {
		return s
	}
	lower := strings.ToLower(host)
	if !strings.ContainsAny(host, ".:") && lower != "localhost" {
		return s
	}
	return lower + "/" + rest
}

// String renders the canonical form of the reference.
func (r Reference) String() string {
	var b strings.Builder
	b.WriteString(r.Repository())
	if r.Tag != "" {
		b.WriteString(":" + r.Tag)
	}
	if r.Digest != "" {
		b.WriteString("@" + r.Digest)
	}
	return b.String()
}

// Repository renders the host and the path, without tag or digest.
func (r Reference) Repository() string {
	return Path{Host: r.Host, Segments: r.Segments}.String()
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

// String renders the host and the segments.
func (p Path) String() string {
	if len(p.Segments) == 0 {
		return p.Host
	}
	return p.Host + "/" + strings.Join(p.Segments, "/")
}

// ParsePath parses and normalises a `repository` or `repositoryGroup` value, written fully
// qualified with no tag and no digest. A repository needs at least one path segment; a
// group may be a host alone.
//
// The host is normalised as a reference's is (lowercased, index.docker.io is docker.io), and a
// single-segment docker.io repository gets its `library` segment, so that
// `repository: docker.io/nginx` names what `nginx` pulls. A group gets no `library`
// segment: `docker.io/bitnami` holds `bitnami/redis`.
func ParsePath(s string, kind Kind) (Path, error) {
	if strings.Contains(s, "@") {
		return Path{}, fmt.Errorf("path %q carries a digest: the digest comes from the pod", s)
	}
	host, rest, hasPath := strings.Cut(s, "/")
	if hasPath && strings.Contains(rest, ":") {
		return Path{}, fmt.Errorf("path %q carries a tag: the tag comes from the pod", s)
	}
	if !hostPattern.MatchString(host) {
		return Path{}, fmt.Errorf("path %q does not start with a registry host: write it fully qualified", s)
	}
	if kind == Repository && !hasPath {
		return Path{}, fmt.Errorf("repository %q is a host alone, not a repository", s)
	}
	pattern := groupPattern
	if kind == Repository {
		pattern = repositoryPattern
	}
	if !pattern.MatchString(s) {
		return Path{}, fmt.Errorf("path %q is not a valid repository path", s)
	}

	// Hosts are case-insensitive: lowercase before the Docker Hub rules, as Parse does.
	host = strings.ToLower(host)
	p := Path{Host: host}
	if host == legacyDockerHub {
		p.Host = dockerHub
	}
	if hasPath {
		p.Segments = strings.Split(rest, "/")
	}
	if kind == Repository && p.Host == dockerHub && len(p.Segments) == 1 {
		p.Segments = []string{dockerHubLibrary, p.Segments[0]}
	}
	return p, nil
}
