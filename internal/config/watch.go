package config

import (
	"context"
	"path/filepath"
	"reflect"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// reloadErrors counts the reloads rejected because the file did not parse or validate.
var reloadErrors = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "kuik_config_reload_errors_total",
	Help: "Reloads of the global config file rejected because it did not parse or validate",
})

func init() {
	metrics.Registry.MustRegister(reloadErrors)
}

// Watch reloads the file on change until ctx is done, and calls onChange with every config
// that validates and differs from the current one. It watches the file's directory: a
// ConfigMap mount replaces the file by swapping a symlink. A rejected reload keeps the
// current config, and is logged and counted. clusterID and metrics are read at startup only:
// a change to them is logged and ignored until restart.
func Watch(ctx context.Context, path string, current *Config, onChange func(*Config)) error {
	log := logf.FromContext(ctx).WithValues("path", path)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-watcher.Errors:
			log.Error(err, "Failed to watch the global config")
		case <-watcher.Events:
			next, err := Load(path)
			if err != nil {
				reloadErrors.Inc()
				log.Error(err, "Rejected global config reload, kept the previous config")
				continue
			}
			if next.ClusterID != current.ClusterID {
				log.Info("Ignored clusterID change until restart", "clusterID", current.ClusterID, "ignored", next.ClusterID)
				next.ClusterID = current.ClusterID
			}
			if next.Metrics != current.Metrics {
				log.Info("Ignored metrics change until restart")
				next.Metrics = current.Metrics
			}
			if reflect.DeepEqual(next, current) {
				continue
			}
			current = next
			log.Info("Reloaded global config")
			onChange(next)
		}
	}
}
