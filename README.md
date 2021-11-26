# docker-cache-registry

## Installation

Cert-manager is used to issue TLS certificate for the mutating webhook. It is thus required to install it first.

1. [Install](https://cert-manager.io/docs/installation/) cert-manager.
1. Install the helm chart `helm upgrade --install --namespace dcr-system dcr ./helm/docker-cache-registry/ --set=image.tag=latest`.
1. Apply the kustomize configuration `k apply -k ./config/default`.
