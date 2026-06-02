---
title: kube-image-keeper
head:
  - tag: title
    content: kube-image-keeper by Enix
description: Container image routing, mirroring and replication for Kubernetes.
template: splash
pagefind: false
hero:
  tagline: kuik (pronounced "quick") keeps your container images available, routed and replicated across registries.
  actions:
    - text: View on GitHub
      link: https://github.com/enix/kube-image-keeper
      icon: external
      attrs:
        target: _blank
        rel: noopener
---

## What is kuik?

**kube-image-keeper** (kuik) is a Kubernetes operator providing container image routing, mirroring (caching) and replication. It intercepts Pod creation via a mutating webhook and rewrites container images to the first available alternative from a prioritized list defined via CRDs.
