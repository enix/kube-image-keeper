# docker-cache-registry

## Installation

1. Install [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) on your cluster.
1. Create a `values.yaml` file and add the following yaml snippet replacing `{IP_ADDRESS}` by an ip address of a node of your cluster.
1. Install the helm chart `helm install dcr ./helm/docker-cache-registry/ --values=./values.yaml --set=image.tag=debug-017`.

```yaml
cacheProxyEndpoint: proxy.{IP_ADDRESS}.nip.io

tugger:
  rules:
    - pattern: ^jainishshah17/tugger.*
    - pattern: ^enix/docker-cache-registry.*
    - pattern: ^registry.*
    - pattern: (.*)
      replacement: proxy.{IP_ADDRESS}.nip.io/$1
```
