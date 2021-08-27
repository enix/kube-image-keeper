# docker-cache-registry

## Installation

1. Install the helm chart `helm upgrade --install --namespace dcr-system dcr ./helm/docker-cache-registry/ --set=image.tag=latest`.
1. Apply the kustomize configuration `k apply -k ./config/default`.
