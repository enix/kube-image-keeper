package fakeregistry

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/ref"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// Parsed is a registry request reduced to what rules match on.
type Parsed struct {
	Kind   api.Kind
	Repo   string
	Tag    string
	Digest string
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Classify maps a request to its endpoint kind, repo and reference.
func Classify(method, urlPath string) (Parsed, bool) {
	switch urlPath {
	case "/v2", "/v2/":
		return Parsed{Kind: api.KindPing}, true
	case "/v2/_catalog":
		return Parsed{Kind: api.KindCatalog}, true
	case "/token":
		return Parsed{Kind: api.KindToken}, true
	}
	p, ok := strings.CutPrefix(urlPath, "/v2/")
	if !ok {
		return Parsed{}, false
	}

	if repo, ok := strings.CutSuffix(p, "/tags/list"); ok {
		return withRepo(Parsed{Kind: api.KindTagsList}, repo)
	}
	if i := strings.LastIndex(p, "/blobs/uploads"); i >= 0 {
		if rest := p[i+len("/blobs/uploads"):]; rest == "" || (rest[0] == '/' && !strings.Contains(rest[1:], "/") && rest != "/." && rest != "/..") {
			return withRepo(Parsed{Kind: api.KindUpload}, p[:i])
		}
	}
	if repo, reference, ok := cutLast(p, "/manifests/"); ok {
		var out Parsed
		switch {
		case digestRe.MatchString(reference):
			out = Parsed{Kind: api.KindManifestByDigest, Digest: reference}
		case ref.ValidTag(reference):
			out = Parsed{Kind: api.KindManifestByTag, Tag: reference}
		default:
			return Parsed{}, false
		}
		switch method {
		case http.MethodPut:
			out.Kind = api.KindManifestPut
		case http.MethodDelete:
			out.Kind = api.KindDelete
		}
		return withRepo(out, repo)
	}
	if repo, reference, ok := cutLast(p, "/blobs/"); ok && digestRe.MatchString(reference) {
		out := Parsed{Kind: api.KindBlob, Digest: reference}
		if method == http.MethodDelete {
			out.Kind = api.KindDelete
		}
		return withRepo(out, repo)
	}
	return Parsed{}, false
}

// cutLast splits s around the last sep, requiring a non-empty reference
// without '/' after it.
func cutLast(s, sep string) (before, reference string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return "", "", false
	}
	reference = s[i+len(sep):]
	if reference == "" || strings.Contains(reference, "/") {
		return "", "", false
	}
	return s[:i], reference, true
}

func withRepo(p Parsed, repo string) (Parsed, bool) {
	if !ref.ValidRepo(repo) {
		return Parsed{}, false
	}
	p.Repo = repo
	return p, true
}
