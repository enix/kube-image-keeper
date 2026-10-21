package main

import (
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// newSecretCache builds the Secret cache shared by the processes' readers of the cluster
// resource namespace.
func newSecretCache(_ ctrl.Manager, _ string) (cache.Cache, error) {
	return nil, errors.New("not implemented")
}
