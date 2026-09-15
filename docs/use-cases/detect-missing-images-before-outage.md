---
description: Monitor image availability across registries and get alerted before ImagePullBackOff reaches production.
sidebar:
  order: 1
---

# Detect missing images before outage

This documentation will help you configure Kuik in order to monitor image availability, enable supervision and alerting, and therefore avoid the typical `ImagePullBackoff` error.

## Best suited for

- You plan a maintenance which will reschedule a lot of pods on new workers
- You plan a Kubernetes upgrade
- You have a lot of legacy images deployed on your cluster

## Benefits

You will have an exhaustive list of missing images.
You will be able to rebuild your registry in advance, and avoid `ImagePullBackoff` which is usually a synonym of a service outage

## Implementation

### Kuik custom resource to use

TODO

### Configuration example

TODO
