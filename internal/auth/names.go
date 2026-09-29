package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// injectedSecretPrefix is reserved to the syncer in every namespace.
	injectedSecretPrefix = "kuik-inject-"
	// maxCRNameLength is what a Secret name leaves to the CR name after `kuik-inject-`, the
	// longest kind that injects (`imagealternative`) and a separator: 253 - 29.
	maxCRNameLength = 224
	// hashLength is the number of hex characters of the hash appended to a truncated name.
	hashLength = 8
)

// InjectedSecretName is the name of the Secret the syncer injects for a routing resource,
// derived from its identity alone: `kuik-inject-<kind lowercased>-<CR name>`. A CR name past
// 224 characters is truncated and a short hash of the whole name is appended, so the name
// stays a valid Secret name and two CRs differing past the cut get different ones. See
// docs/v3/architecture.md, "The name of an injected Secret".
func InjectedSecretName(kind, crName string) string {
	prefix := injectedSecretPrefix + strings.ToLower(kind) + "-"
	if len(crName) <= maxCRNameLength {
		return prefix + crName
	}
	sum := sha256.Sum256([]byte(crName))
	// A DNS subdomain label ends with an alphanumeric character, so the cut cannot end on a
	// separator.
	truncated := strings.TrimRight(crName[:maxCRNameLength-1-hashLength], ".-")
	return prefix + truncated + "-" + hex.EncodeToString(sum[:])[:hashLength]
}
