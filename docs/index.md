---
title: kube-image-keeper
head:
  - tag: title
    content: kube-image-keeper by Enix
description: Container image routing, mirroring and replication for Kubernetes.
template: doc
pagefind: false
hero:
  tagline: kuik (pronounced "quick") keeps your container images available, routed and replicated across registries.
  actions:
    - text: Get started
      link: /2.2/configuration/
      icon: right-arrow
      variant: primary
    - text: View on GitHub
      link: https://github.com/enix/kube-image-keeper
      icon: external
      attrs:
        target: _blank
        rel: noopener
---

## What is kuik?

**kube-image-keeper** (kuik) is a Kubernetes operator providing container image routing, mirroring (caching) and replication. It intercepts Pod creation via a mutating webhook and rewrites container images to the first available alternative from a prioritized list defined via CRDs.

## Explore the docs

- A detailed explanation of all [Kuik Custom Resources](./crds.md)
- A reference for the [operator configuration file](./configuration.md) (routing, monitoring, metrics)
- Kuik manages multiple alternatives of an image and selects the best-suited one. You might want to learn more about the [Priority mechanism](./image-routing.md)
- A migration path from [Kuik v1 to Kuik v2](./v1-to-v2-migration-path.md)
- A collection of documented [use cases](./use-cases/)
- A [development guide](./development.md)
