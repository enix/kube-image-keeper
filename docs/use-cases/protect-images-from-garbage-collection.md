---
description: Back up the images currently used by running Pods to a second registry before upstream garbage collection removes them.
sidebar:
  order: 3
---

# Protect images from garbage collection

This documentation will help you configure Kuik in order to "backup" useful (used by a running Pod) images on another registry, prior to a garbage collection on your origin registry.

## Best suited for

- You configured a garbage collect on your origin registry, and you feel that it is too aggressive in terms of image deletion.
- You have plenty of images (outdated, prior versions, development version).
- You would like to keep only the subset of useful images in your production registry.

## Benefits

- Kuik will ensure that useful images stays replicated on a new registry.
- and will garbage collect images that are no longer used in your Kubernetes cluster.

## Implementation

### Kuik custom resource to use

TODO

### Configuration example

TODO
