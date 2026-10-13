package kuik

// entryCredentials are the credentials of one entry offering a reference, and its transport.
type entryCredentials[T any] struct {
	insecure bool
	auths    []T
}

// checkAttempt is one request of a check: its transport and the credentials sent there.
type checkAttempt[T any] struct {
	insecure bool
	auths    []T
}

// checkPlan orders the requests of a check.
func checkPlan[T any](_ []entryCredentials[T], _ []T, _ bool) []checkAttempt[T] {
	return nil
}
