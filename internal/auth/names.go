package auth

// InjectedSecretName is the name of the Secret the syncer injects for a routing resource,
// derived from its identity alone. See docs/v3/architecture.md, "The name of an injected
// Secret".
func InjectedSecretName(kind, crName string) string {
	return ""
}
