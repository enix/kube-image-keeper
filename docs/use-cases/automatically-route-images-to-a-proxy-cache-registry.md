---
description: Rewrite Pod images on the fly to a proxy cache registry, with no spec changes to your workloads.
---

# Automatically route images to a proxy cache registry

This documentation will help you configure Kuik in order to simplify a proxy cache registry implementation in Kubernetes.
In other words, Kuik will automatically rewrite image paths to use a proxy cache; without requiring any `spec` customization (Deployment, StatefulSet, ...).

## Best suited for

- You already have setup a proxy cache registry (like Harbor or Gitlab proxy cache) but do not know how to use it
- You do not want to review all workloads deployments (and change their image path)

## Benefits

Kuik will manage the burden of rerouting calls to your proxy cache

## Implementation

### Kuik custom resource to use

TODO

### Configuration example

TODO
