---
description: Route around rate limits and outages by replicating or mirroring the images you depend on.
sidebar:
  order: 2
---

# Overcome public registry limitations

This documentation will help you configure Kuik in order to overcome public registry limitations.

## Best suited for

- You face an image pull rate limit
- Your upstream registry is no longer available
- Your images are already pushed to multiple registries, or you let kuik copy them there with an
  [ImageMirror](../crds.md#imagemirror)

## Benefits

Your Kubernetes cluster will **seamlessly** pull images from another registry and avoid listed difficulties.

kuik itself stays within the quota: it reads each registry at the pace you configure, and one
request serves every monitor and mirror tracking the same image. See
[Registry pacing](../concepts/pacing.md).

## Implementation

### Kuik custom resource to use

TODO

### Configuration example

TODO
