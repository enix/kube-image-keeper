package main

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// loggerOptions are the zap options every process starts from, before its flags.
func loggerOptions() zap.Options {
	return zap.Options{Development: true}
}

// logStarting logs the start of the manager with the build information.
func logStarting(log logr.Logger, proc processName) {}
