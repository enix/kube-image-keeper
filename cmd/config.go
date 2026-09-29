package main

import (
	"context"

	"github.com/enix/kube-image-keeper/internal/config"
)

// configWatch reloads the global config file for as long as the manager runs, on every
// replica: a replica waiting for the lease must hold the current config when it takes over.
type configWatch struct {
	path    string
	current *config.Config
	// onChange are the consumers of the process, called with every reloaded config.
	onChange []func(*config.Config)
}

// Start watches the file until ctx is done.
func (w *configWatch) Start(ctx context.Context) error {
	return config.Watch(ctx, w.path, w.current, func(c *config.Config) {
		for _, f := range w.onChange {
			f(c)
		}
	})
}

// NeedLeaderElection is false: every replica reloads.
func (w *configWatch) NeedLeaderElection() bool {
	return false
}
