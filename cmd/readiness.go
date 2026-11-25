package main

import (
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// webhookServerReady reports ready once the webhook server accepts TLS connections: it fails
// until the server has started, then dials the server's own port at each probe. The kubelet's
// probe period is the retry.
func webhookServerReady(server webhook.Server) healthz.Checker {
	return server.StartedChecker()
}
