package registry

import (
	"net/http"
	"strings"
)

// IsRateLimited returns true if the registry response headers indicate the
// rate limit has been reached ("ratelimit-remaining: 0;...").
func IsRateLimited(headers http.Header) bool {
	return strings.HasPrefix(headers.Get("ratelimit-remaining"), "0;")
}
