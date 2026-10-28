---
description: Monitor image availability across registries and get alerted before ImagePullBackOff reaches production.
sidebar:
  order: 1
---

# Detect missing images before an outage

kuik checks, in the background, that every image running in your cluster can still be pulled from its registry, and reports the ones that cannot. You learn that an image is gone while its pods still run from the node cache, not when they are rescheduled and fail with `ImagePullBackOff`.

## Best suited for

- You plan a maintenance that will reschedule many pods on new nodes
- You plan a Kubernetes upgrade, or a node pool replacement
- You run many legacy images, from registries or projects nobody watches any more
- Your registries run a garbage collection or a retention policy

## Benefits

- A live list of the images that can no longer be pulled, with the reason and the pods that run them (the first 500 in the status)
- A Prometheus series per missing image, to alert on, which covers every image whatever the size of the list
- Time to rebuild or restore an image before the pods that need it restart

## How it works

An [`ImageMonitor`](../crds.md#imagemonitor) tracks the images of the pods it selects: running ones, and for a week after the last pod went (`unusedImageRetention`), so that a CronJob's image stays checked between runs. A pod that goes while the reconciler is down is not seen going, and its image is not retained. It sends a manifest `HEAD` to each image's registry, with the pods' own pull secrets, and lists the images whose check fails in `status.unavailableImages`, with a reason:

| Reason | Meaning |
| ------ | ------- |
| `ManifestNotFound` | The tag or digest no longer exists: deleted, or garbage collected |
| `Unauthorized` | No credential kuik holds can read it, see [Registry credentials](../configuration.md#registry-credentials) |
| `QuotaExceeded` | The registry rate-limits: kuik will check again on its next round |
| `Unreachable` | The registry does not answer, or its TLS certificate is not trusted |

Checks are paced per registry, one image per `check.interval` (1 minute by default), to keep kuik's share of a rate limit small: each image is checked once per round, and a monitor alone on a registry with 120 images takes 2 hours per round. Other resources reading the same registry, restarts and retained images make a round longer. See [Registry pacing](../concepts/pacing.md).

The monitor only reads: it never changes a pod.

## Set it up

1. [Install kuik](../installation.md).

2. Create an `ImageMonitor` over the whole cluster:

   ```yaml
   apiVersion: kuik.enix.io/v1alpha1
   kind: ImageMonitor
   metadata:
     name: cluster-images
   spec: {}
   ```

   To watch only some workloads, restrict it with `namespaceSelector` and `podSelector`, see [Selecting workloads](../crds.md#selecting-workloads).

3. For private images, kuik reads with the pull secrets of the pods that run them. If you installed kuik with the chart value `secretAccess.mode: restricted`, or if your nodes pull with credentials the pods do not carry, declare the credentials in [`fallbackAuth`](../configuration.md#fallbackauth):

   ```yaml
   # values.yaml
   config:
     fallbackAuth:
     - repositoryGroup: registry.example.com
       secretRef:
         name: registry-example-read   # a docker-registry Secret in kuik's namespace
   ```

4. For registries you own, which do not ration reads, shorten the round (see [Registry pacing](../concepts/pacing.md)):

   ```yaml
   # values.yaml
   config:
     registries:
       registry.example.com:
         check:
           interval: 5s
   ```

## Check the result

The monitor counts the images it tracks, the unavailable ones and the drifted ones:

```bash
kubectl get imagemonitors
# NAME             TRACKED   UNAVAILABLE   DRIFTED   READY   AGE
# cluster-images   214       3             1         True    5d
```

The unavailable ones, with their reason, since when, and how many pods run them:

```bash
kubectl get imagemonitor cluster-images -o yaml
```

```yaml
status:
  unavailableImages:
  - ref: registry.example.com/legacy/billing:2.3.1
    reason: ManifestNotFound
    since: "2026-10-21T08:12:44Z"
    pods: 2
```

Before a maintenance, wait until every image was checked at least once: `status.checks.registries[].cycleDuration` is the length of the last round on each registry.

An image whose registry refuses an anonymous check emits an `AnonymousAccessDenied` event on the monitor: it is likely private, and no credential covers it. Declare one in `fallbackAuth`.

## Alert

The reconciler exports its metrics on port `8080` of the `kube-image-keeper-reconciler-metrics` Service, in kuik's namespace. With the Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kuik-reconciler
  namespace: kuik-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/component: reconciler
  endpoints:
  - port: metrics
```

`kuik_image_origin_unavailable` exists only while an image is unavailable, one series per image, whatever the size of the status list. Alert on its presence:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kuik-images
  namespace: kuik-system
spec:
  groups:
  - name: kuik
    rules:
    - alert: ContainerImageUnavailable
      expr: kuik_image_origin_unavailable{reason=~"ManifestNotFound|Unauthorized"}
      for: 15m
      annotations:
        summary: "{{ $labels.image }} can no longer be pulled ({{ $labels.reason }})"
```

The other series, and the events, are listed in [Events, metrics and logs](../observability.md).

## Good to know

- Under `driftDetection` (on by default), the monitor also reports the tags whose upstream digest moved away from the one your pods run: a rescheduled pod would run different code. They are listed in `status.driftedImages`, and emit an `ImageTagDrifted` event.
- A monitor finds the problem; it does not fix it. To keep a copy of your images that pods fall back to, add an [`ImageMirror`](./protect-images-from-garbage-collection.md), or route to an existing copy with an [`ImageAlternative`](./overcome-public-registry-limitations.md).
