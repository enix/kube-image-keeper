---
description: The events, Prometheus metrics and logs kuik emits, what each one means and how to read them.
---

# Events, metrics and logs

kuik reports on four channels besides the status of its resources:

| Channel | Holds | Read it with |
| ------- | ----- | ------------ |
| [Pod annotations](./crds.md#what-the-webhook-records-on-a-pod) | what the webhook decided for each container of a pod | `kubectl get pod -o yaml` |
| [Events](#events) | the moment something changed, on the object you will inspect | `kubectl describe`, `kubectl get events` |
| [Metrics](#metrics) | the status aggregates over time, and the anomalies as series | Prometheus |
| [Logs](#logs) | every decision of a process, every deletion, and the detail behind them at `debug` | `kubectl logs`, [hl](#reading-the-logs) |

Events expire after the API server's `event-ttl` (1 hour by default): they are not an audit
log. The [logs](#logs) are, once a log pipeline keeps them: a pod's own logs go with it. Alert on
the metrics.

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
| `ResourceNotReady` | Warning | The `Ready` condition goes `False`. The message carries its reason: `InvalidConfig`, `SecretNotFound`, `SecretMalformed`, and `RegistryDeleteUnsupported` on an `ImageMirror` whose destination refuses tag deletion, or manifest deletion under `cleanup.deleteByDigest` |
| `ResourceReady` | Normal | `Ready` goes back to `True` |
| `PullSecretInjectionFailed` | Warning | The secret syncer could not resolve a credential of the resource for one namespace, and wrote the [injected Secret](./concepts/pull-secrets.md) without it. The message carries the reason (`SecretNotFound`, `SecretMalformed`), the entry's path and the namespace. Emitted by the secret syncer, once while the failure lasts |

### On an ImageMonitor

| Reason | Type | Emitted when |
| ------ | ---- | ------------ |
| `AnonymousAccessDenied` | Warning | A registry refused an anonymous check with `Unauthorized`: the image is likely private, and neither a pod's pull secret nor `fallbackAuth` covers it. Declare a [`fallbackAuth`](./configuration.md#fallbackauth) entry. Also for a private image kept in `status.retainedImages`, whose pods are gone with their pull secrets (see [Known limits](./known-limits.md#monitoring)). Never for an image a `fallbackAuth` entry matches, even one whose Secret is missing or malformed: `Ready` reports that. Once while the image stays in `status.unavailableImages`, whatever reason it fails with meanwhile; a restart does not announce it again, unless the image was beyond the cap of that list or its last persisted reason was not `Unauthorized`. Only the monitor whose ring sent the request emits it, not the ones that reuse its verdict |
| `AlternativeUnusable` | Warning | A registry refused a monitored alternative with `Unauthorized`: it cannot be offered as a candidate until a credential is declared, in the `auth` of its entry or in [`fallbackAuth`](./configuration.md#fallbackauth). Each time the failure of the alternative becomes `Unauthorized`, not again while it stays so. A restart does not announce it again, unless the alternative was beyond the cap of `status.unavailableAlternatives` or its last persisted reason was not `Unauthorized`. An alternative failing for any other reason emits nothing |

An image going unavailable for any other reason emits nothing: read `status.unavailableImages`,
or alert on `kuik_image_origin_unavailable`.

### On an ImageMirror

| Reason | Type | Emitted when |
| ------ | ---- | ------------ |
| `ImageCopied` | Normal | An image was copied to the destination for the first time. A new mirror emits a burst of them |
| `ImageRecopied` | Warning | A copy the destination held went missing and was copied again: something outside kuik deleted it while pods may be routed to it |
| `ImageResynced` | Normal | Under `driftPolicy: Sync`, a destination tag was moved onto the upstream's new digest |
| `CopyOutOfDate` | Warning | Under `driftPolicy: Warn`, the upstream tag moved away from the copy, which is left as it is. Once per drift |
| `ImageCopyFailed` | Warning | A copy failed, retries included. Failures reported in the same status write that share a reason and the registry of their image make one event, naming the image, or how many images failed under their common prefix. Copies are paced one image per `copy.interval` and host, and each failure is reported as it happens: expect about one event per failed copy, at most one per `copy.interval` and host |
| `ImageUnrecoverable` | Warning | No source can supply an image any more (`SourceNotFound` on the origin and every alternative). Pods still running it do so from a node cache. Once, when the copy starts failing that way |
| `OrphanTagFound` | Warning | The sweep found a tag of this cluster no tracked image accounts for. It is deleted once its retention elapsed. Once per tag |
| `ImageDeleted` | Normal | A tag was deleted after its retention elapsed, or was already gone. Under `cleanup.deleteByDigest`, also for each tag of a manifest deleted with another tag of this cluster in the same pass |
| `ImageDeletionFailed` | Warning | The destination refused a tag deletion, by the sweep or by the cleanup of a deleted `ImageMirror`, or under `cleanup.deleteByDigest` failed the `HEAD` of a tag or the manifest deletion. A destination that does not support the deletion also sets `Ready` `False` with reason `RegistryDeleteUnsupported`; on a live `ImageMirror`, the event fires at the first refusal only, not at the tries that follow (10 seconds later, doubling up to 1 hour), and again at a refusal after an accepted try or a change of the spec. The cleanup of a deleted `ImageMirror` emits it at each try. A tag kept because another tag shares its manifest emits nothing |
| `WaitingForPods` | Normal | A deleted `ImageMirror` waits for the live pods running its copies to go, keeping its tags and its injected pull Secrets: since Kubernetes 1.35 a restarted container checks its pull Secret again. The message gives how many pods hold it. When the wait starts and each time that count changes, not at each try; a restart does not announce it again while the count is unchanged. The `DeletionBlocked` condition is `True` with reason `PodsRunningCopies` meanwhile, its message naming up to 5 of those pods (see [Deleting an ImageMirror](./crds.md#deleting-an-imagemirror) for its other reasons); `Ready` does not change. The reconciler logs `Held a deleted ImageMirror: live pods run its copies` at `info` with `count` and `pods`, see [An ImageMirror stays Terminating](./guides/troubleshooting.md#an-imagemirror-stays-terminating) |

## Scraping

Each process serves its series over plain HTTP on `:8080/metrics`, and the chart creates one
Service per process on that port, named `metrics`: `<fullname>-webhook-metrics`,
`<fullname>-reconciler-metrics` and `<fullname>-secret-syncer-metrics`, in the release
namespace. Every series of the [metrics](#metrics) below is exported by the reconciler unless
its row says otherwise.

The chart can also create a ServiceMonitor for
[prometheus-operator](https://prometheus-operator.dev), that selects the three Services: a
Prometheus whose `serviceMonitorSelector` matches it scrapes them. The `job` label of the targets is
the Service name, so the three processes stay apart.

| Value | Default | Role |
| ----- | ------- | ---- |
| `monitoring.serviceMonitor.enabled` | `false` | Create the ServiceMonitor |
| `monitoring.serviceMonitor.namespace` | the release namespace | Where the ServiceMonitor lives, for a Prometheus that watches one namespace. The Services it selects stay in the release namespace |
| `monitoring.serviceMonitor.labels` | `{}` | Labels added to it, e.g. the `release` label of a kube-prometheus-stack that selects on it |
| `monitoring.serviceMonitor.interval`, `scrapeTimeout` | the Prometheus defaults | Scrape interval and timeout |
| `monitoring.serviceMonitor.relabelings`, `metricRelabelings` | `[]` | Relabelings of the targets before scraping, and of the samples after |

## Metrics

The reconciler, the webhook and the secret syncer export their series on the metrics endpoint
of their process.
Every series about a resource carries `kind` and `name`: aggregate on both, never on `name`
alone, since an `ImageAlternative` and an `ImageMirror` may share a name.

A call the webhook never answered (a timeout, a TLS error, no replica) is in none of these
series: the API server admits the pod unchanged and is the only one to count it. Read its own
metrics, under the name of the webhook:
`apiserver_admission_webhook_fail_open_count{name="pods.kuik.enix.io"}` counts the calls that
failed and were ignored, `apiserver_admission_webhook_request_total` with the same `name`
counts every call, by `code`.

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

### Mirror

An `ImageMirror` exports its copy side, `status.images.copy`, and what it does to its
destination.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_images_tracked{kind, name, reference, state}` | gauge | References the mirror holds at its destination (`reference="copy"`), by `state`: `running`, `standby`, `retained` |
| `kuik_images_checked{kind, name, reference, state}` | gauge | The same references by verdict of the last destination pass or copy: `available`, `unavailable` |
| `kuik_mirror_tags_orphan{kind, name}` | gauge | Tags of this cluster the sweep found that no tracked reference accounts for, held before deletion. Counted in tags |
| `kuik_mirror_copies_total{kind, name, reason}` | counter | Images pushed to the destination, by `reason`: `Initial`, `Recopy` after a copy went missing, `Resync` after an upstream digest moved |
| `kuik_mirror_tags_deleted_total{kind, name, reason}` | counter | Tags deleted once their retention elapsed, by `reason`: `Unused`, `Orphan` |
| `kuik_mirror_self_checked_timestamp_seconds{kind, name}` | gauge | End of the last whole destination pass, `status.selfChecked` |
| `kuik_mirror_copy_duration_seconds{kind, name}` | histogram | Seconds one copy took. Off by default: one series per bucket per mirror. Enable it with `metrics.copyDuration: true` in the [global config](./configuration.md) |

`sum without(state) (kuik_images_tracked{reference="copy"})` is `status.images.copy.tracked`.

### Monitor

An `ImageMonitor` exports the origin references of the pods it selects, `status.images.origin`, and under `monitorAlternatives` their alternatives, `status.images.alternatives` (`reference="alternatives"`). Turning `monitorAlternatives` off deletes the `alternatives` series.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_images_tracked{kind, name, reference, state}` | gauge | Origin references the monitor tracks (`reference="origin"`), or their alternatives (`reference="alternatives"`), by `state`: `running`, `standby`, `retained` |
| `kuik_images_checked{kind, name, reference, state}` | gauge | The same references by verdict of their last check: `available`, `unavailable` |

`sum without(state) (kuik_images_tracked{reference="origin"})` is `status.images.origin.tracked`.
`sum without(state) (kuik_images_checked{reference="origin"})` falls short of it by the images
the rings have not reached yet. A restart keeps `unavailable` at least as the status left it
until the rings have checked every image again, and forgets the `available` verdicts: they come back as
the rings turn, within one `cycleDuration`.

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
| `kuik_image_copy_failed{kind, name, image, registry, reason}` | `ImageMirror.status.failedImageCopies`. `1` per image whose copy fails; `registry` is the side that failed: the source on `SourceNotFound`, `SourceUnreachable`, the destination on `PushRejected`, `DestinationUnreachable`, whichever side answered on `Unauthorized`, `QuotaExceeded` |
| `kuik_image_origin_unavailable{kind, name, image, registry, reason}` | `ImageMonitor.status.unavailableImages`. `1` per tracked origin whose last check failed, with its `reason`: `ManifestNotFound`, `Unauthorized`, `QuotaExceeded`, `Unreachable`. No event announces it: alert on this series. After a restart, the series of an image beyond the cap of the list comes back once its ring reaches it |
| `kuik_image_alternative_unavailable{kind, name, image, registry, derived_from, via, reason}` | `ImageMonitor.status.unavailableAlternatives`. `1` per monitored alternative whose last check failed and origin it stands for: `image` is the alternative, `registry` its host, `derived_from` the origin, `via` the `ImageAlternative` offering it, as `ImageAlternative/<name>`. After a restart, the series of an alternative beyond the cap of the list comes back once its ring reaches it |
| `kuik_image_drifted{kind, name, image, registry}` | `ImageMirror.status.driftedImages`. `1` per tag whose upstream digest moved away from the copy, under `driftPolicy: Warn`, and briefly under `Sync` until the resync. `image` is the origin reference |

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
| `kuik_registry_interval_seconds{registry, operation}` | gauge | `check.interval` (`Check`) and `copy.interval` (`Copy`) of a host, as currently loaded, and `mirror.destinationScan.interval` (`Scan`) of a destination an ImageMirror scans, gone once no mirror scans it |

Alert on a lap against its shared lap rather than on a literal: see
[Watching the pace](./concepts/pacing.md#watching-the-pace).

### Registry requests

The reconciler and the webhook export this counter, each for the requests it sent.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_registry_requests_total{registry, operation, result}` | counter | Requests kuik sent to a source registry, by `operation` (`Check`, `Copy`) and outcome: `Ok`, `ManifestNotFound`, `Unauthorized`, `QuotaExceeded`, `Unreachable`. Each credential tried counts its own request. Under `Copy`: the source check that opens a copy, the read of the source manifest, and the blob read that ends a failed copy; the blobs read successfully are not counted. A mirror destination is not counted |

A rising `QuotaExceeded` on one `registry` says that host rate-limits kuik; `operation` says
whether the checks or the copies are refused. A host that rations requests per second, refusing
copies after their first few requests, calls for a
[`requestInterval`](./concepts/pacing.md#spacing-the-requests).

### Pull secrets

The secret syncer exports this counter, see [Injected pull secrets](./concepts/pull-secrets.md).

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_secret_applies_total{result}` | counter | Pull-secret applies, by outcome: `Applied`, `Noop` when the API server stored nothing new, `Failed` |

The syncer applies blind, so a healthy steady state is mostly `Noop`. A rate of `Applied` that
stays high means something is flapping. The first apply of each Secret after the syncer starts
counts as `Applied`, even when it changes nothing: expect one per Secret at startup.

### Process

Each of the three processes exports these about itself.

| Metric | Type | Value |
| ------ | ---- | ----- |
| `kuik_build_info{version, revision, built, goversion, goos, goarch}` | gauge | Always `1`, labelled with the build: the kuik version, the git revision, the build date and the Go toolchain. The same labels open the logs of the process (`Starting manager`). Join on it to tell a kuik target from another: `and on (cluster, instance) kuik_build_info` |
| `kuik_config_reload_errors_total` | counter | Reloads of the global config file rejected because it did not parse or validate: the process keeps the previous config, and only a log line says why otherwise |

## Alerts

The chart ships the kuik alerts as a PrometheusRule for
[prometheus-operator](https://prometheus-operator.dev), off by default:
`monitoring.alerts.enabled: true` creates it. The chart does not check for the CRD: without
prometheus-operator, the install fails. A kuik process down is left to the `TargetDown` alert of
kube-prometheus-stack, or its equivalent.

Without the chart, use `helm/kube-image-keeper/files/prometheus-rules.yaml` as is: a Prometheus
rule file for `rule_files`, whose `groups` also fit the `spec` of a PrometheusRule.

| Value | Role |
| ----- | ---- |
| `monitoring.alerts.namespace`, `labels`, `annotations` | Where the PrometheusRule lives, its labels and annotations: add the label the `ruleSelector` of your Prometheus expects |
| `monitoring.alerts.groups.<group>: false` | Leave out a group of the table below |
| `monitoring.alerts.disabled.<Alert>: true` | Leave out one alert |
| `monitoring.alerts.customRules.<Alert>` | Change the `severity` or the `for` of one alert: `KuikRegistryUnreachable: { severity: critical, for: 15m }` |
| `monitoring.alerts.additionalRuleLabels`, `additionalRuleAnnotations`, `additionalRuleGroupLabels.<group>` | Labels and annotations added to every alert, or labels added to the alerts of one group, to route them in Alertmanager. A `severity` in `additionalRuleLabels` replaces the severity of every alert |
| `monitoring.alerts.thresholds.<threshold>` | Change a threshold of the table below |
| `monitoring.alerts.targetLabels` | The labels that identify one scrape target, dropped by every aggregation of the rules. Add the labels a relabeling sets per target |

`critical` means pods will not start, or an irreversible deletion.
`warning` means an action is due, with no urgency.
`info` is a debt that wakes nobody: no alert of the chart uses it by default, `customRules` can
set it.

The `description` of each alert says what happened and what to do.

| Alert | Group | Severity | Threshold |
| ----- | ----- | -------- | --------- |
| `KuikConfigReloadFailed` | `process` | `warning` | |
| `KuikResourceNotReady` | `process` | `warning` | |
| `KuikAlternativesExhausted` | `routing` | `critical` | |
| `KuikRewriteConceded` | `routing` | `warning` | |
| `KuikRewriteStale` | `routing` | `warning` | |
| `KuikManyOriginsUnavailable` | `availability` | `warning` | `manyOriginsUnavailable: 10` |
| `KuikAlternativeUnauthorized` | `availability` | `warning` | |
| `KuikImageUnrecoverable` | `mirror` | `critical` | |
| `KuikImageCopyFailing` | `mirror` | `warning` | |
| `KuikMirrorRecopying` | `mirror` | `warning` | |
| `KuikMirrorSelfCheckStale` | `mirror` | `warning` | `mirrorSelfCheckStaleIntervals: 3` |
| `KuikMirrorOrphanTagsGrowing` | `mirror` | `warning` | `orphanTagsGrowing: 50` |
| `KuikMirrorManyOrphanDeletions` | `mirror` | `critical` | `manyOrphanDeletions: 20` |
| `KuikCheckLapOverrunning` | `pacing` | `warning` | |
| `KuikCheckLapStalled` | `pacing` | `critical` | |
| `KuikCopyBudgetSaturated` | `pacing` | `warning` | |
| `KuikRegistryQuotaExceeded` | `registry` | `warning` | |
| `KuikRegistryUnreachable` | `registry` | `warning` | |
| `KuikRegistryUnauthorized` | `registry` | `warning` | |
| `KuikStatusListNearCapacity` | `status` | `warning` | |
| `KuikStatusListDropping` | `status` | `critical` | |
| `KuikSecretApplyFailing` | `secrets` | `critical` | |
| `KuikSecretApplyFlapping` | `secrets` | `warning` | |
| `KuikWebhookLatencyHigh` | `controller` | `critical` | `webhookLatencyP99Seconds: 1` |
| `KuikReconcileErrors` | `controller` | `warning` | |
| `KuikWorkqueueBacklog` | `controller` | `warning` | `workqueueBacklogSeconds: 300` |

## Dashboard

The chart ships a Grafana dashboard, off by default: `monitoring.dashboard.enabled: true`
creates a ConfigMap, `<fullname>-dashboard`, holding it under the key `kuik.json`, with the
label `grafana_dashboard: "1"` the sidecar of kube-prometheus-stack watches. The dashboard is
written in the schema v2 of Grafana (dynamic dashboards: tabs, auto grids, panels hidden when
empty): it needs **Grafana 13**. An older Grafana refuses it with `dashboard appears to be in v2
format`.

| Value | Default | Role |
| ----- | ------- | ---- |
| `monitoring.dashboard.enabled` | `false` | Create the ConfigMap |
| `monitoring.dashboard.namespace` | the release namespace | Where the ConfigMap lives, the namespace the sidecar watches |
| `monitoring.dashboard.labels` | `{grafana_dashboard: "1"}` | Labels of the ConfigMap, added to the common labels of the chart. Labels set here replace the default rather than merge with it |
| `monitoring.dashboard.annotations` | `{}` | Annotations of the ConfigMap, e.g. `grafana_folder: kuik` for the sidecar to file it in a folder |
| `monitoring.dashboard.datasource` | `default` | Prometheus datasource the dashboard opens on, by name or uid; `default` is the default datasource of the Grafana organization |
| `monitoring.dashboard.extraMatchers` | `""` | PromQL matchers appended to the cluster matcher of every query and variable |
| `monitoring.dashboard.extraVariables` | `[]` | Variables inserted between `datasource` and `cluster`, to narrow the clusters offered |
| `monitoring.dashboard.adhocFilters` | `false` | Add the `filters` variable, Grafana's ad hoc filters: a free filter on any label, applied to every query |

The dashboard is one page of six tabs, general to specific, linked to this page, every panel
carrying a description of what it shows and what it cannot:

- **Overview**: the processes (replicas up, leader, config reloads, version), the health
  figures of the resources, the images and the routing, every anomaly by family and in one
  table grouped by family, the registries by host and result.
- **Routing**: the share of the containers each resource selects by state and of its pods it
  rewrote, its reach against its scope, the live pods of each exhausted candidate, active
  fallback, conceded and stale rewrite, the rewrites by policy.
- **Mirror**: what each `ImageMirror` holds at its destination against what it wants, the
  failing and unrecoverable copies, then what it does to the destination: copies, recopies,
  deletions, orphan tags, the copy duration histogram. An activity panel with no series is
  hidden.
- **Monitor**: the population of origins and alternatives, their verdicts over time, the
  retention, the unavailable origins and alternatives.
- **Pacing**: whether the configured pace of each registry keeps up, see
  [Registry pacing](./concepts/pacing.md): one row per registry, unfolding into its rings, with
  each ring's last lap and lap in progress against the longest the rings sharing its host allow;
  its size and last lap side by side, the intervals each host is read at, the copy budget.
- **Processes**: the webhook admission latency over the time range and codes, the reconcile
  errors and workqueues, the secret syncer applies, the status lists, fullest first.

The variables, in order: `datasource`, `cluster` (one cluster at a time; a single-cluster
Prometheus without the label offers one empty value, which matches its series), `kind`,
`name`, `registry`, `reason`, a free `filters` on any label when `adhocFilters` is on, and a
hidden `job` that restricts the controller-runtime panels to the kuik targets.
Every query carries the cluster matcher `cluster="$cluster"`, the resource-scoped ones `kind` and `name`, the
registry-scoped ones `registry`. Graphs and stats aggregate with `without`, so a label a
relabeling adds reaches the legends. Tables aggregate with `by` over the labels of their series,
which leaves the value as it is and keeps an added label out of the columns.

### Several teams or clusters

A Prometheus that holds several clusters under labels of its own gets them as dropdowns.
`extraVariables` inserts query variables before `cluster`; `extraMatchers` appends a matcher
to every query and to the `label_values` of every variable after `cluster`, and the `cluster`
variable itself becomes `label_values(kuik_build_info{<extraMatchers>}, cluster)`, so each
dropdown offers the values of the team selected before it. The extra variables are never
patched: write their query in full.

```yaml
monitoring:
  dashboard:
    enabled: true
    extraVariables:
      - name: team
        label: Team
        query: label_values(kuik_build_info, team)
        multi: true
        includeAll: true
    extraMatchers: team=~"$team"
```

### Editing the dashboard

`helm/kube-image-keeper/files/dashboard-kuik.json` is the source, in the form Grafana exports it
("Export as code", JSON), with no template syntax inside: the chart patches it structurally at
render time. To change it, import the file into a Grafana 13 (Dashboards, New, Import), edit
it there, export it as code and replace the file. Keep the conventions above: the v2 wrapper,
the cluster matcher in every query, `without` in graphs and `by` in tables, no `job` literal, the rate windows, a
description on every panel, the variable order.

## Logs

Deployed by the chart, each process writes one JSON object per line on its standard error:

```json
{"level":"info","ts":"2026-10-07T10:00:00.000Z","logger":"setup","msg":"Starting manager","process":"reconciler","version":"3.0.0-alpha.3","revision":"252a096","built":"2026-10-06T09:00:00Z","goversion":"go1.26.0","goos":"linux","goarch":"amd64"}
```

- `level`, `ts` (ISO 8601), `logger` and `msg` come first, then the key-value pairs of the line.
  `logger` is `setup` for the start-up lines, and the lowercase kind (`imagealternative`,
  `imagemirror`, `imagemonitor`) for the reconciler's lines about a resource. A line with no
  logger name, such as a line of a library kuik runs on, has no `logger`.
- The keys are stable: `image`, `registry`, `resource` (`<kind>/<name>`), `reason`, and a
  Kubernetes object under its lowercase kind (`pod`, `secret`) as `{"name":"...","namespace":"..."}`.
  Every reconciler line about a resource carries `resource`; a check or a copy also carries the
  `image` and the `registry` it reads. An alternative names the `ImageAlternative` offering it
  under `via`, as the `kuik_image_alternative_unavailable` series does.
- The first line of a process is `Starting manager`, with the same build labels as
  `kuik_build_info`.
- Every deletion kuik makes itself is logged at `info`; what the garbage collector removes
  through an owner reference is not.
- The content of a Secret is never logged, only its namespace and name.
- `error` is for what an operator must act on. An expected failure, such as an unreachable
  registry, is logged at `info` or `debug` with an `error` key.

### Verbosity

The `verbosity` value of the chart sets the level, for all processes at the root or for one in
its block (`webhook`, `reconciler`, `secretSyncer`):

| Level | Shows |
| ----- | ----- |
| `info` (default) | decisions and state changes |
| `debug` | also the detail behind them: each candidate tried, each check |
| `error` | only what needs an operator |

```bash
helm upgrade kube-image-keeper oci://quay.io/enix/charts/kube-image-keeper:$VERSION \
  --namespace kuik-system --reuse-values --set reconciler.verbosity=debug
```

### Reading the logs

JSON lines are made for log pipelines (Loki, Elasticsearch...). To read them in a terminal, we
recommend [hl](https://github.com/pamburus/hl): it renders JSON and logfmt lines and passes any
other line through unchanged, so it is safe on the logs of any pod. All pods of a process at once:

```bash
kubectl logs -n kuik-system -l app.kubernetes.io/component=reconciler -f --prefix \
  | hl --allow-prefix --paging=never
```

[`jq`](https://jqlang.org) works too: `kubectl logs ... | jq -r '"\(.ts) \(.level) \(.msg)"'`.

#### In Sofka

[Sofka](https://github.com/nklmilojevic/sofka) is the Kubernetes TUI we recommend. Since
0.31.1, its log view renders each record on one row: time, level, message, then the other
fields. Make it the view a log session starts in, in `~/.config/sofka/config.toml`:

```toml
[logs]
json_view = "record"
```

Lines that are not JSON stay raw, so the setting is safe for every pod. In a log view, `J`
cycles raw, record and indented JSON.

#### In k9s

[k9s](https://k9scli.io) cannot render the records itself. Its community plugin
[`log-hl.yaml`](https://github.com/derailed/k9s/blob/master/plugins/log-hl.yaml) follows the
logs of a pod or a container through hl: add its entries to `~/.config/k9s/plugins.yaml`, then
press `Shift-L`.

## Readiness

Each process serves `/readyz` on its health probe port. Until every check passes, the replica
stays out of its Service. `/readyz?verbose` lists the checks with their state. Besides the
`readyz` ping, the webhook has 3 checks:

| Check | Passes once |
| ----- | ----------- |
| `cluster-id` | the webhook has read the [cluster ID](./configuration.md#cluster-id) the reconciler stores |
| `registry-tls` | the CA Secrets of the [registry TLS](./configuration.md#registry-tls) entries are read. The reconciler has this check too |
| `routing-index` | the webhook has listed the `ImageAlternative`, `ImageMirror` and `ImageMonitor` objects of the cluster, none included, and built the index it routes with |

Readiness keeps the Service's traffic away from a replica; it does not change what a request
that still reaches it gets. Before its cluster ID is read or its routing index first built, the
replica admits the pod unchanged; with only `registry-tls` failing, it routes as usual. A replica
that stays unready logs why:

- `cluster-id`: `Waiting for ConfigMap` until the reconciler creates it
- `routing-index`: when the webhook may not list one of the 3 kinds, the client-go line
  `failed to list ... forbidden`, repeated, and the replica stays unready until its RBAC is
  fixed. When one of the 3 CRDs is missing, the webhook does not start: `Failed to run manager`,
  and the pod restarts in a loop
