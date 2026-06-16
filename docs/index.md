---
title: kube-image-keeper
head:
  - tag: title
    content: kube-image-keeper by Enix
description: Container image routing, mirroring and replication for Kubernetes.
template: doc
pagefind: false
hero:
  tagline: kuik (pronounced /kwɪk/, like "quick") is the shortname of kube-image-keeper.
  actions:
    - text: Get started
      link: /installation/
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

✅ Its primary objective is to **maximize the availability of Pod images** strictly within the Kubernetes cluster it runs on.

✅ Its secondary goal is to ensure **bulletproof reliability** by keeping the manipulation of Kubernetes primitives to an absolute minimum.

## Under the hood

It relies on three core mechanisms:
- [**Image routing**](/concepts/image-routing/): rewrites Pod image paths on the fly during their creation to redirect them to a functional registry.
- **Image copy**: mirror images **used by the local cluster** accross registries, building a virtual, highly available registry.
- **Image monitoring**: continuously tracks the availability of Pod images **used within the local cluster** across various registries.

Note : image routing is performed at Pod creation by a lightweight `MutatingWebhook` that automatically rewrites the image path whenever the source registry becomes unavailable.

Developed by Enix, kube-image-keeper is a battle-tested solution currently running in production across multiple Kubernetes clusters.

## Explore the docs

- A detailed explanation of all [Kuik Custom Resources](./crds.md)
- A reference for the [operator configuration file](./configuration.md) (routing, monitoring, metrics)
- A collection of documented [use cases](./use-cases/)
- A [development guide](./guides/development.md)
