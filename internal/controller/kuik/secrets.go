package kuik

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// secretCache returns shared, the cache of the Secrets of namespace the process shares, or
// builds one and adds it to mgr when there is none. The reconcilers read Secrets in the
// cluster resource namespace only, so they watch them through a cache of that namespace alone.
func secretCache(mgr ctrl.Manager, namespace string, shared cache.Cache) (cache.Cache, error) {
	if shared != nil {
		return shared, nil
	}
	secrets, err := cache.New(mgr.GetConfig(), cache.Options{
		Scheme:            mgr.GetScheme(),
		Mapper:            mgr.GetRESTMapper(),
		DefaultNamespaces: map[string]cache.Config{namespace: {}},
	})
	if err != nil {
		return nil, err
	}
	if err := mgr.Add(secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}
