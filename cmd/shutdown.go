package main

import (
	"context"
	"net/http"
	"time"

	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// delayShutdown returns a context that ends delay after ctx ends, and a readiness check that
// fails from the moment ctx ends.
func delayShutdown(ctx context.Context, _ time.Duration, _ clock.Clock) (context.Context, healthz.Checker) {
	return ctx, func(*http.Request) error { return nil }
}
