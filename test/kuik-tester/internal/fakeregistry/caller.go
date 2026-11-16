package fakeregistry

import "strings"

// callerClass classifies a User-Agent into a coarse caller name.
func callerClass(userAgent string) string {
	switch {
	case strings.Contains(userAgent, "crane/"):
		return "crane"
	case strings.Contains(userAgent, "kuik"), strings.Contains(userAgent, "go-containerregistry"):
		return "kuik"
	case strings.Contains(userAgent, "containerd"):
		return "containerd"
	}
	return "other"
}
