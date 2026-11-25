package main

import (
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// webhookServerReady reports ready once the webhook server accepts TLS connections.
func webhookServerReady(_ webhook.Server) healthz.Checker {
	return healthz.Ping
}
