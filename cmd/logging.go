package main

import (
	"runtime"

	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/enix/kube-image-keeper/internal/config"
	"github.com/enix/kube-image-keeper/internal/info"
)

// loggerOptions are the zap options every process starts from, before its flags: JSON in
// production, console text with -zap-devel. A stacktrace on every error buries the message
// it explains, so only DPanic and above carry one (controller-runtime#2173).
func loggerOptions() zap.Options {
	return zap.Options{
		StacktraceLevel: zapcore.DPanicLevel,
		TimeEncoder:     zapcore.ISO8601TimeEncoder,
	}
}

// startUp logs the start of the process, its first line, then loads the global config at
// path.
func startUp(log logr.Logger, proc processName, path, clusterResourceNamespace string) (*config.Config, error) {
	logStarting(log, proc)
	// The file is required: a process does not start on a config nobody validated.
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	log.Info("Loaded global config", "path", path, "clusterResourceNamespace", clusterResourceNamespace)
	return cfg, nil
}

// logStarting logs the start of the manager with the build information, the same labels as
// kuik_build_info.
func logStarting(log logr.Logger, proc processName) {
	log.Info("Starting manager", "process", proc,
		"version", info.Version,
		"revision", info.Revision,
		"built", info.BuildDateTime,
		"goversion", runtime.Version(),
		"goos", runtime.GOOS,
		"goarch", runtime.GOARCH)
}
