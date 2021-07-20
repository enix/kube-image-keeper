# docker-cache-registry

## Installation

1. Install [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) on your cluster.
1. Install the helm chart `helm install dcr ./helm/docker-cache-registry/ --values=./values.yaml --set=image.tag=debug-017`.
