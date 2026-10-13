package kuik

import "slices"

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

// checkPlan orders the requests of a check, so that no credential crosses plain HTTP unless
// plain HTTP is the only way the reference is offered. The first request goes over HTTPS with
// the auth of the secure entries, in candidate order, then the shared credentials (the pull
// secrets of the pods, fallbackAuth). When an entry is insecure, a second request goes over
// plain HTTP with the auth of the insecure entries, and the shared credentials only when no
// secure entry offers the reference: an all-insecure registry answers on plain HTTP alone, as
// the webhook probes that candidate. An origin is offered securely: its pods pull it over HTTPS.
func checkPlan[T any](entries []entryCredentials[T], shared []T, origin bool) []checkAttempt[T] {
	var secure, insecure []T
	offeredSecurely, offeredInsecurely := origin || len(entries) == 0, false
	for _, e := range entries {
		if e.insecure {
			offeredInsecurely = true
			insecure = append(insecure, e.auths...)
			continue
		}
		offeredSecurely = true
		secure = append(secure, e.auths...)
	}
	var plan []checkAttempt[T]
	if offeredSecurely {
		plan = append(plan, checkAttempt[T]{auths: slices.Concat(secure, shared)})
	}
	if offeredInsecurely {
		if !offeredSecurely {
			insecure = slices.Concat(insecure, shared)
		}
		plan = append(plan, checkAttempt[T]{insecure: true, auths: insecure})
	}
	return plan
}
