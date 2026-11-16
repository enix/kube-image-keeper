// Package match implements the glob matching used by rule targets and settings.
package match

import (
	"path"
	"strings"
)

// Label matches a single host label or tag with path.Match semantics.
// An empty pattern matches everything.
func Label(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	ok, _ := path.Match(pattern, s)
	return ok
}

// Repo matches a repository path segment by segment: "**" matches zero or
// more segments, any other segment is matched with path.Match. An empty
// pattern matches everything.
func Repo(pattern, repo string) bool {
	if pattern == "" {
		return true
	}
	return segments(strings.Split(pattern, "/"), strings.Split(repo, "/"))
}

// segments reports whether pat matches segs. ok[i][j] tells whether pat[i:]
// matches segs[j:], which makes the match O(len(pat)*len(segs)).
func segments(pat, segs []string) bool {
	m, n := len(pat), len(segs)
	ok := make([][]bool, m+1)
	for i := range ok {
		ok[i] = make([]bool, n+1)
	}
	ok[m][n] = true
	for i := m - 1; i >= 0; i-- {
		for j := n; j >= 0; j-- {
			if pat[i] == "**" {
				ok[i][j] = ok[i+1][j] || (j < n && ok[i][j+1])
				continue
			}
			if j == n {
				continue
			}
			if hit, _ := path.Match(pat[i], segs[j]); hit {
				ok[i][j] = ok[i+1][j+1]
			}
		}
	}
	return ok[0][0]
}

// TrimPrefix removes prefix from repo when it covers whole segments.
func TrimPrefix(repo, prefix string) (rest string, ok bool) {
	if repo == prefix {
		return "", true
	}
	if strings.HasPrefix(repo, prefix+"/") {
		return repo[len(prefix)+1:], true
	}
	return "", false
}

// Valid reports path.ErrBadPattern if any segment of pattern is malformed.
func Valid(pattern string) error {
	for seg := range strings.SplitSeq(pattern, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}
	return nil
}

// Literal reports whether s is non-empty and free of glob metacharacters
// (any of "*?[\").
func Literal(s string) bool {
	return s != "" && !strings.ContainsAny(s, `*?[\`)
}
