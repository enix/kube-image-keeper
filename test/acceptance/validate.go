package main

import (
	"context"
	"fmt"
	"strings"
)

// examplePrivate fills ${PRIVATE_REPO} and ${PULL_AUTH} for validate: values of the right
// shape, never sent to a cluster. ${KUIK_NAMESPACE} comes from --kuik-namespace, as for setup.
var examplePrivate = privateFlags{
	privateRepo: "registry.example.com/kuik-test/nginx/nginx-unprivileged",
	pullAuth:    "example-pull-auth",
}

// runValidate checks every fixture without a cluster: its since line, its strict decoding with
// example values, and the guard on each of its objects. It prints one line per fixture and goes
// on after a failure, so one run lists them all.
func runValidate(_ context.Context, e *env, _ []string) error {
	names, err := fixtureNames()
	if err != nil {
		return err
	}
	failed := 0
	for _, name := range names {
		summary, err := e.validateFixture(name)
		if err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n", name, err)
			continue
		}
		fmt.Printf("ok   %s: %s\n", name, summary)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d fixtures failed", failed, len(names))
	}
	return nil
}

// validateFixture returns what the fixture holds: its since version and its objects.
func (e *env) validateFixture(name string) (string, error) {
	raw, err := fixtures.ReadFile(fixtureDir + name)
	if err != nil {
		return "", err
	}
	since, err := fixtureSince(name, raw)
	if err != nil {
		return "", err
	}
	expanded, err := expand(name, raw, e.vars(&examplePrivate))
	if err != nil {
		return "", err
	}
	objs, err := e.decode(name, expanded)
	if err != nil {
		return "", err
	}
	described := make([]string, len(objs))
	e.pullAuth = examplePrivate.pullAuth
	for i, obj := range objs {
		if err := e.guard(obj); err != nil {
			return "", err
		}
		if err := e.checkWrite(obj); err != nil {
			return "", err
		}
		described[i] = describe(obj)
	}
	return fmt.Sprintf("since %s, %s", strings.TrimPrefix(since, "v"), strings.Join(described, ", ")), nil
}
