package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// errShuttingDown is what the readiness check reports once the stop signal came.
var errShuttingDown = errors.New("shutting down")

// delayShutdown returns a context that ends delay after ctx ends, and a readiness check that
// fails from the moment ctx ends. It is kube-apiserver's --shutdown-delay-duration: a pod
// being deleted stays a Service endpoint until the endpoint controller and every kube-proxy
// have seen it go, and a server that stopped listening by then refuses the requests still
// sent to it. Serving through the delay lets the endpoint go first.
func delayShutdown(ctx context.Context, delay time.Duration, clk clock.Clock) (context.Context, healthz.Checker) {
	delayed, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var stopping atomic.Bool
	go func() {
		<-ctx.Done()
		stopping.Store(true)
		setupLog.Info("Received the stop signal, serving until the shutdown delay ends", "delay", delay)
		if delay > 0 {
			<-clk.After(delay)
		}
		cancel()
	}()

	return delayed, func(*http.Request) error {
		if stopping.Load() {
			return errShuttingDown
		}
		return nil
	}
}
