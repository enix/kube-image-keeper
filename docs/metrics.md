# Monitoring & Metrics

To assist with operations and provide a visualization of kube-image-keeper activities, Prometheus metrics are exposed from the three components (proxy, controller and registry).

If you are deploying kube-image-keeper with Helm, both PodMonitor and ServiceMonitor can be configured through the following values `controllers.podMonitor.create=true`, `proxy.podMonitor.create=true` and `registry.serviceMonitor.create=true`.

## Exported Metrics

### Controller

| Metric | Description |
|--------|-------------|
| kube_image_keeper_controller_build_info | Provide informations about controller version |
| kube_image_keeper_controller_cached_images | Count of all cached images expired or not |
| kube_image_keeper_controller_image_put_in_cache_total | Count of all cached images since the bootstrap |
| kube_image_keeper_controller_image_removed_from_cache_total | Count of all images removed from the cache |
| kube_image_keeper_controller_is_leader | Return 1 if the pod is leader |
| kube_image_keeper_controller_up | Return 1 if the controller is running |

### Proxy

| Metric | Description |
|--------|-------------|
| kube_image_keeper_proxy_build_info | Provide informations about proxy version |
| kube_image_keeper_proxy_http_requests_total | Provide information about cache hit and http requests |


### Registry

| Metric | Description |
|--------|-------------|
| registry_http_* | Provide information about registry http requests |
| registry_storage_* | Provide information about storage operations |


