---
description: The events and Prometheus metrics kuik emits, what each one means and when it fires.
---

# Events and metrics

kuik reports on three channels besides the status of its resources:

| Channel | Holds | Read it with |
| ------- | ----- | ------------ |
| [Pod annotations](./crds.md#what-the-webhook-records-on-a-pod) | what the webhook decided for each container of a pod | `kubectl get pod -o yaml` |
| [Events](#events) | the moment something changed, on the object you will inspect | `kubectl describe`, `kubectl get events` |
| [Metrics](#metrics) | the status aggregates over time, and the anomalies as series | Prometheus |

Events expire after the API server's `event-ttl` (1 hour by default): they are not an audit
log. Alert on the metrics.

## Events

### On a pod

The reconciler emits these on the pod they concern, where `kubectl describe pod` shows them
next to the pull error they explain.

| Reason | Type | Emitted when |
| ------ | ---- | ------------ |
| `ImageFallback` | Normal | Under `rewritePolicy: OnFailure`, the original did not answer and an alternative did. The message names the original, the reference kuik placed and the resource that supplied it. `Always` rewrites emit nothing: they are the steady state |
| `NoAlternativeAvailable` | Warning | The original did not answer and no candidate did either. The pod is left as it is and may still start from the node's cache. One event per container, naming every resource that offered a candidate |
| `RewriteConceded` | Warning | Another mutating webhook replaced the reference kuik had placed, and kuik stood down. Two components dispute one field: narrow the scope of one of them |
| `RewriteStale` | Warning | A container kuik had rewritten was edited after admission (`kubectl set image` on the pod, a controller patching it). The record no longer describes what runs. Roll the workload to send it back through admission |

`ImageFallback`, `NoAlternativeAvailable` and `RewriteConceded` go only to pods created after
the reconciler acquired its lease, so a restart or a leader change does not announce every
live pod again. `RewriteStale` fires once, when the rewrite goes stale, whenever the pod was
created.

### On a resource

| Reason | Type | Emitted when |
| ------ | ---- | ------------ |
| `ResourceNotReady` | Warning | The `Ready` condition goes `False`. The message carries its reason: `InvalidConfig`, `SecretNotFound`, `SecretMalformed` |
| `ResourceReady` | Normal | `Ready` goes back to `True` |

## Metrics

The reconciler and the webhook export their series on the metrics endpoint of their process.
Every series about a resource carries `kind` and `name`: aggregate on both, never on `name`
alone, since an `ImageAlternative` and an `ImageMirror` may share a name.

### Routing aggregates

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_routing_containers{kind, name, state}` | gauge | Containers of the pods a resource selects, by `state`: `untouched`, `rewritten`, `conceded`, `stale`, `noAlternatives`, the fields of `status.containers` |
| `kuik_routing_pods_tracked{kind, name}` | gauge | Live pods the resource selects, static pods aside: kuik never routes them |
| `kuik_routing_pods_rewritten{kind, name}` | gauge | Live pods carrying at least one standing rewrite by the resource |
| `kuik_routing_rewrites_total{kind, name, policy}` | counter | Container images rewritten at admission, by `policy` (`Always`, `OnFailure`). Exported by the webhook |
| `kuik_routing_alternatives_exhausted_total{kind, name}` | counter | Containers left untouched at admission because no candidate answered, counted once per resource that offered one. Exported by the webhook |

`sum without(state) (kuik_routing_containers)` is `status.containers.tracked`. Selectors may
overlap, so the pod gauges do not sum across resources; the `rewritten`, `conceded` and
`stale` containers do, exactly one resource counting each.

### Anomalies

These series exist only while their anomaly does: alert on their presence. The four `_pods`
series count live pods, `image` being the origin reference and `registry` its host. They
hold every affected image, whatever the cap on the status lists.

| Metric | Status counterpart |
| ------ | ------------------ |
| `kuik_fallback_active_pods{kind, name, image, registry}` | `status.activeFallbacks` |
| `kuik_alternatives_exhausted_pods{kind, name, image, registry}` | `status.noAlternatives`. Every resource that offered a candidate counts the pod: do not sum |
| `kuik_rewrite_conceded_pods{kind, name, image, registry}` | `status.concededRewrites` |
| `kuik_rewrite_stale_pods{kind, name, image, registry}` | `status.staleRewrites` |

`kuik_resource_not_ready{kind, name, reason}` is `1` while the `Ready` condition of a
resource is `False`, carrying its reason.

### Status capacity

The anomaly lists of a status hold at most 500 entries, the oldest first. These series say
when one is filling up, before and after it starts leaving entries out.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_status_list_entries{kind, name, list}` | gauge | Entries written in the list |
| `kuik_status_list_capacity{kind, name, list}` | gauge | The cap of the list |
| `kuik_status_list_dropped_total{kind, name, list}` | counter | Entries the list could not hold, each counted once while it stays out |

`list` is the status field name, as in `status.truncated`. Alert on
`kuik_status_list_entries / kuik_status_list_capacity` rather than on a literal: the
`ListCapacityPressure` condition goes `True` from 80%.

### Scheduling health

Whether the configured pace keeps up, see [Registry pacing](./concepts/pacing.md). The
reconciler exports these series.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_check_cycle_duration_seconds{kind, name, registry}` | gauge | Last completed lap of the ring a resource turns over a registry, `status.checks.registries[].cycleDuration`. Absent until a first lap completes |
| `kuik_check_cycle_started_timestamp_seconds{kind, name, registry}` | gauge | Start of the lap in progress, `cycleStarted` |
| `kuik_check_ring_images{kind, name, registry}` | gauge | Size of the ring, `images` |
| `kuik_registry_interval_seconds{registry, operation}` | gauge | `check.interval` (`Check`) and `copy.interval` (`Copy`) of a host, as currently loaded |

Alert on a lap against its best case rather than on a literal: see
[Watching the pace](./concepts/pacing.md#watching-the-pace).
