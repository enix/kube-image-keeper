# AGENTS.md

This file provides guidance for AI coding agents (Claude Code, Cursor, Aider, OpenAI Codex CLI, etc.) when working with code in this repository.

## Project Overview

kube-image-keeper (kuik) v2 is a Kubernetes operator providing container image routing, mirroring, and replication. Built with Go and Kubebuilder v4 (controller-runtime). It intercepts Pod creation via a mutating webhook and rewrites container images to the first available alternative from a prioritized list defined via CRDs.

## Common Commands

The Makefile is the source of truth — run `make help` for the full list. A few non-obvious things:

- `go test ./internal/controller/kuik -run TestName -v` — run a single unit test
- `make test-e2e` needs a running Kind cluster (see [`docs/guides/development.md`](docs/guides/development.md))

For the documentation site (Astro / Starlight), commands live in [`website/`](website/) — see its [README](website/README.md). Local preview: `cd website && npm install && npm run dev`.

## Documentation

User-facing documentation lives under [`docs/`](docs/) at the repository root and is published at **[kuik.enix.io](https://kuik.enix.io)**. The markdown files are the single source of truth (read them alongside the code when working on the corresponding areas):

- [`docs/crds.md`](docs/crds.md): CRD reference with all fields and semantics
- [`docs/image-routing.md`](docs/image-routing.md): deep dive into the priority system and webhook routing logic
- [`docs/configuration.md`](docs/configuration.md): all config fields with defaults and examples
- [`docs/resource-filtering.md`](docs/resource-filtering.md): image / namespace / pod filtering semantics
- [`docs/guides/development.md`](docs/guides/development.md): local development setup and workflow

The same files render both on GitHub (browse `docs/`) and on the Astro Starlight site. Author them in GitHub-flavored markdown; the website build converts the GitHub-specific bits to Starlight at build time (see [How the docs site is built](#how-the-docs-site-is-built)).

### Markdown conventions for the docs site

Write for GitHub first; the build adapts. When editing or adding docs under `docs/`:

- Give every page its title as a leading `# H1` (the first body line), not a frontmatter `title:`. It reads naturally on GitHub, and the `sync-docs` build lifts that H1 into the Starlight frontmatter `title:` and strips it from the body (so Starlight does not render a duplicate heading). Add a `description:` in frontmatter; it doubles as the SEO `<meta>` description and is shown on the use-cases index cards. A page with only a `description:` (and no `title:`) is expected; GitHub renders that small frontmatter table above the `# H1`. Website-only overlay pages keep a frontmatter `title:` and are left untouched by the lift.
- Use relative markdown links between docs: `[Label selectors](./resource-filtering.md#label-and-annotation-selector-syntax)`. They work on GitHub, and the build rewrites them to site routes (`/resource-filtering/#...`). Do NOT write rendered routes like `/resource-filtering/`; they break on GitHub.
- Use GitHub alert syntax for callouts: `> [!NOTE]`, `> [!TIP]`, `> [!WARNING]`, `> [!IMPORTANT]`, `> [!CAUTION]`. They render natively on GitHub, and the build converts them to Starlight asides (note / tip / caution / danger). Do NOT use Starlight's `:::note` syntax; it shows as raw text on GitHub.
- New use-case files dropped under `docs/use-cases/` are automatically picked up by the use-cases index page and the sidebar on the next build.

### How the docs site is built

The site (in [`website/`](website/), Astro Starlight) does not read `docs/` directly. A `sync-docs` integration in [`website/astro.config.mjs`](website/astro.config.mjs) copies `docs/`, [`website/src/content/versioned-docs/`](website/src/content/versioned-docs/), and [`website/src/content/overlay/`](website/src/content/overlay/) into `website/src/content/docs/` (gitignored, generated) before content collections load. The overlay holds website-only pages that must not live in `docs/` (the homepage `index.md` and the use-cases landing `use-cases/index.mdx`); it is copied last, so it wins on path conflicts. While copying, `sync-docs` also lifts each page's leading `# H1` into the frontmatter `title:` Starlight requires (and removes it from the body), so docs can carry their title as a GitHub-friendly H1; pages that already define a frontmatter `title:` are left as-is.

The docs are **versioned** with the [`starlight-versions`](https://starlight-versions.vercel.app) plugin: the current `docs/` tree is served at the site root (labelled `main`), and each archived version is a committed frozen snapshot under `website/src/content/versioned-docs/<slug>/` (served at `/<slug>/`) with its sidebar in `website/src/content/versions/<slug>.json`. The full "add a new version" workflow lives in [`website/README.md`](website/README.md#documentation-versioning).

This indirection exists because Starlight's sidebar `autogenerate`, asides, and routing all assume the conventional `src/content/docs` collection directory; loading `docs/` directly (via a custom loader or a symlink) breaks them. Two markdown plugins then bridge GitHub and Starlight syntax: `remark-github-admonitions-to-directives` (alerts to asides) and `astro-rehype-relative-markdown-links` (relative `.md` links to routes).

In dev (`npm run dev` in `website/`), a chokidar watcher in the integration mirrors changes from `docs/` and the overlay into the generated directory, so edits hot-reload. Caveats: run only one `astro dev` at a time (two instances fight over the generated directory), and editing `website/astro.config.mjs` or `website/scripts/sync-docs.mjs` restarts the dev server.

## Architecture

### Custom Resources (5 CRDs)

| CRD | Scope | Purpose |
| --- | ----- | ------- |
| **ClusterImageSetMirror / ImageSetMirror** | Cluster / Namespaced | Routes images to mirrored registries and triggers automated image copying |
| **ClusterReplicatedImageSet / ReplicatedImageSet** | Cluster / Namespaced | Routes images to alternative upstream registries (checks availability, doesn't copy) |
| **ClusterImageSetAvailability** | Cluster | Monitors image availability across the cluster, tracks per-image status |

Every resource is scoped by a unified `spec.filter` (image / label / annotation selectors; cluster-scoped variants add a `namespace` dimension). It replaces the removed `spec.podFilter` / `spec.namespaceFilter` fields and supersedes the deprecated `spec.imageFilter`. `(Cluster)ReplicatedImageSet` ignores the filter's `image` dimension (image selection is per-upstream via `spec.upstreams[].imageFilter`). See [`docs/resource-filtering.md`](docs/resource-filtering.md).

### Entry Point

`cmd/main.go` wires everything into a controller-runtime `Manager`:

- **Webhook server** (`webhook.NewServer`) with TLS — the serving cert is provisioned by cert-manager via `helm/kube-image-keeper/templates/webhook-certificate.yaml` (and `config/certmanager/` for the kustomize layout); `MutatingWebhookConfiguration` carries `cert-manager.io/inject-ca-from` for CA injection
- **Metrics server** (controller-runtime's `metricsserver`) with optional TLS
- **Leader election** under ID `e69ca865.enix.io` (off by default; toggled by `--leader-elect`)
- **Reconcilers** registered against the manager (see Core Packages below)

New work touching reconciler setup, webhook config, manager flags, or TLS plumbing starts here.

### Webhook: Image Rewriting Flow

`internal/webhook/core/v1/pod_webhook.go` is the critical path. On every Pod CREATE:

1. **Global pod filter** — drops pods matching `config.SkipLabels` / `config.SkipAnnotations`; skips mirror pods and pods already annotated with `kuik.enix.io/original-images`
2. **Per-container filtering** — skips digest-pinned images (`@sha256:...`) and `imagePullPolicy: Never` containers (configurable)
3. **CR collection** — fetches all applicable CISM/ISM/CRIS/RIS, filtered by each resource's unified `spec.filter` (pod labels / annotations, plus `namespace` for cluster-scoped kinds)
4. **Alternative building** — creates a prioritized list including the original image; each entry's position is determined by the two-level priority system (see below)
5. **Availability checking** — uses `parallel.FirstSuccessful()` with singleflight deduplication and two 1-second TTL caches: `checkCache` (per-image availability boolean) and `alternativeCache` (the resolved alternative for a given original image, which short-circuits re-routing of the same image within the TTL)
6. **Rewriting** — patches Pod containers; stores original images in `kuik.enix.io/original-images` annotation (JSON) to prevent re-processing

### Two-Level Priority System _(see [`docs/image-routing.md`](docs/image-routing.md))_

**Level 1 — CR priority (`spec.priority`, signed int, default 0):**

- Negative: alternatives placed _before_ the original image (override)
- Zero: original tried first, alternatives are fallback
- Positive: alternatives tried after the original with lower priority

**Level 2 — intra-CR priority (`mirrors[].priority` / `upstreams[].priority`, unsigned int, default 0):**

- Lower value = higher priority (same semantics as Linux `nice`)
- Zero preserves YAML declaration order

Default type order when priorities are equal: Original → CISM → ISM → CRIS → RIS

### Core Packages

- **`internal/filter/`** — three components:
  - `PodFilter`: label/annotation selector matching (include AND NOT exclude semantics)
  - `IncludeExcludeFilter`: generic regex-based filter
  - `PrefixFilter`: wraps any `Filter` to strip a registry prefix before matching
  - The unified `spec.filter` (`Filter` / `ClusterFilter`) lives in `api/kuik/v1alpha1/filter_types.go`. Default-allow semantics (inject `.*` when a dimension's `include` is empty) live in `Filter.BuildImageFilter()` (image dimension) and `ClusterFilter.BuildPodMatcher()` (namespace dimension), plus the deprecated `ImageFilterDefinition.Build()`

- **`internal/registry/`** — registry interaction via go-containerregistry. `CheckImageAvailability()` returns typed statuses: `Available`, `NotFound`, `Unreachable`, `InvalidAuth`, `QuotaExceeded`. Two additional `ImageAvailabilityStatus` values (`Scheduled`, `UnavailableSecret`) exist but are set by the availability reconciler — not by `CheckImageAvailability()`. `CopyImage()` does the cross-registry transfer that backs mirroring, using `remote.Write` / `remote.WriteIndex` and honouring `config.Mirroring.Platforms`; it's invoked from `commonimagesetmirror.go`. Handles TLS, insecure registries, pull secrets, and rate-limit detection.

- **`internal/parallel/`** — `FirstSuccessful[P,R]()`: runs `f` concurrently on all params, returns the first successful result in original param order along with prior errors.

- **`internal/config/`** — koanf config from `/etc/kube-image-keeper/config.yaml`. Key fields: `SkipLabels`, `SkipAnnotations`, `Routing.ActiveCheck.Timeout`, `Routing.RewriteOnNeverImagePullPolicy`, `Mirroring.Platforms`, `Monitoring.Registries`. Full reference: [`docs/configuration.md`](docs/configuration.md).

- **`internal/controller/kuik/`** — reconcilers:
  - `ImageSetMirrorReconciler` and `ClusterImageSetMirrorReconciler` — namespaced and cluster-scoped peers, both extending `ImageSetMirrorBaseReconciler` (`commonimagesetmirror.go`) which holds the shared image-copying and stale-mirror-cleanup logic
  - `ClusterImageSetAvailabilityReconciler` — monitors image availability, updates Prometheus metrics, and is the only place where the `Scheduled` and `UnavailableSecret` statuses are written
  - `SecretOwnerReconciler[T client.Object]` — generic reconciler that adds a cleanup finalizer to an owner CR and, on deletion, removes every Secret labelled with the owner's UID (`OwnerUIDLabel`). Currently used only to garbage-collect the pull secrets injected into user namespaces for mirroring/replication; `cmd/main.go` instantiates it for `ClusterImageSetMirror` and `ClusterReplicatedImageSet`

- **`internal/info/`** — build-time version metadata (`Version`, `Revision`, `BuildDateTime`, populated via `-ldflags`) and a Prometheus collector that exposes them under the `kube_image_keeper` metrics namespace.

- **`internal/testsetup/`** — side-effect import for test suites that registers a Gomega custom formatter so `*regexp.Regexp` values render as their source string in failure output. Test suites are Ginkgo/Gomega with envtest; suite files follow `suite_test.go` and load CRDs from `config/crd/bases/`. End-to-end tests live under `test/e2e/`.

### Non-Obvious Behaviors

- **Stale mirror cleanup**: when the webhook gets `NotFound` for a mirrored image, it clears `mirroredAt` in the ISM/CISM status, signaling the controller to re-mirror.
- **Image normalization**: `github.com/distribution/reference.ParseNormalizedNamed()` is used to canonicalize image refs before any comparison.
- **`make manifests`** syncs generated CRDs into both `config/crd/bases/` and `helm/kube-image-keeper/crds/`; always run it after changing CRD types.

## Change Discipline

Any code change that adds, modifies, or removes behaviour must be accompanied by:

- **Tests** — update or add unit/E2E tests covering the affected behaviour
- **Documentation** — update the relevant page under `docs/` and, if applicable, Helm chart values docs or README / AGENTS.md sections. The documentation site (published at [kuik.enix.io](https://kuik.enix.io)) auto-deploys from `main` via [`.github/workflows/website.yaml`](.github/workflows/website.yaml).

## Git Hooks

Lefthook runs `make manifests generate lint-fix` and `markdownlint-cli2` (the latter skipped unless Node.js ≥ 20 is available) on pre-commit, and `make test` on pre-push; conventional commits are enforced. See [`CONTRIBUTING.md`](CONTRIBUTING.md).
