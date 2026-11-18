# kuik website

Documentation site for [kube-image-keeper](https://github.com/enix/kube-image-keeper), built with [Astro Starlight](https://starlight.astro.build/).

## Commands

Run from the `website/` directory:

| Command | Action |
| :--- | :--- |
| `npm install` | Install dependencies |
| `npm run dev` | Start local dev server at `localhost:4321` |
| `npm run build` | Build the production site to `./dist/` |
| `npm run preview` | Preview the build locally before deploying |
| `npm run astro ...` | Run CLI commands like `astro add`, `astro check` |

The site requires **Node.js 24** (matching the CI `withastro/action` config); the [`starlight-versions`](https://starlight-versions.vercel.app) plugin needs Node ≥ 22.

## Documentation versioning

The site serves multiple documentation versions via the [`starlight-versions`](https://starlight-versions.vercel.app) plugin. The set of versions is declared once in [`versions.mjs`](./versions.mjs) and consumed by both [`astro.config.mjs`](./astro.config.mjs) (which feeds `{ slug, label }` to the plugin) and [`scripts/sync-docs.mjs`](./scripts/sync-docs.mjs) (which sources each version's content).

- The **current** version (labelled `v3` in the version picker, the docs for new installs) is the live `../docs` tree, served at the site root (`/configuration/`, `/crds/`, …).
- Each **archived** version's docs live on that version's **maintenance branch** (`ref` in `versions.mjs`, e.g. `2.3.x`) and are served under the version slug (`/2.3/configuration/`, …). There is **no committed copy** of the versioned docs in this branch. The maintenance branch also carries the release-line code, so a version has a single branch for both docs and code, and doc fixes are ordinary commits there.

At build time, `sync-docs` generates the gitignored collection dirs:

- For each version it runs `git archive <ref> docs` and lays the result under `src/content/docs/<slug>/`. The branch's docs are plain GitHub markdown with **no `slug:` frontmatter**; `sync-docs` injects `slug: <slug>/<path>` into each page on disk, because Astro's loader and the relative-link rewriter both github-slugify the path — which would turn `2.3` into `23` and break routes/links.
- That branch also carries `docs/version-config.json` (the version's Starlight sidebar); `sync-docs` lifts it into `src/content/versions/<slug>.json` (the `versions` content collection, see [`src/content.config.ts`](./src/content.config.ts)) and does not render it as a page. Sidebar slugs in it are relative to the version (`"configuration"`, not `"2.3/…"`); the plugin prepends the version slug.
- Subtrees of `../docs` listed in `UNPUBLISHED_DOCS` (in `sync-docs.mjs`, currently `v3/`, the v3 design documents) are **not** copied at all, neither by the full sync nor by the dev watcher. They render on GitHub only: they are not user documentation and they link to non-page files (YAML examples, bare directories) that the site cannot serve and the links validator would reject.
- The website-only [`src/content/overlay/`](./src/content/overlay/) pages (currently the use-cases landing `use-cases/index.mdx`) are copied into **every** version, not just the current one (the homepage is a normal `../docs/index.md` page, so it is per-version automatically). Note the use-cases landing's `getCollection` is not version-scoped, so each version's `/…/use-cases/` index currently lists the **current** version's use cases — the per-version use-case pages themselves are correct and in the sidebar.

Because every configured version already has its docs on disk at build time, the plugin never triggers its built-in "archive the current docs" behaviour. The build therefore needs git history for the maintenance branches — CI's checkout uses `fetch-depth: 0` (see [`.github/workflows/website.yaml`](../.github/workflows/website.yaml)).

### Version messaging (root = v3, archived slugs = maintained for existing installs)

The site root is the `v3` docs, for new installs; root pages carry no version notice. An archived version (`2.3`) is not outdated but still maintained for existing installs, so [`src/components/PageTitle.astro`](./src/components/PageTitle.astro) replaces the plugin's "outdated" notice on its pages with a blue note, shown under the page title: the version is maintained for existing installs, new installs use v3 (a link to the same page at the root, or the root home when it has none) and [Upgrading from v2](../docs/installation.md#upgrading-from-v2) is linked. [`src/components/Banner.astro`](./src/components/Banner.astro) shows the same note as a banner on archived landing pages, which `PageTitle` skips, and suppresses the plugin's splash banner.

The components classify a page by matching its id against the configured version slugs. Overriding `PageTitle` makes the plugin log a one-line "already defined" warning at build: expected, since we render the notice ourselves instead of the plugin's `<VersionNotice />`.

[`src/content/i18n/en.json`](./src/content/i18n/en.json) additionally rewords the plugin's **search**-modal version strings (still rendered by the plugin) so they point archived-version readers to the v3 docs for new installs instead of "the latest version for up-to-date results".

### Add a new archived version

To publish version `X.Y` (e.g. when cutting a release):

1. Create the version's **maintenance branch `X.Y.x`** and retarget the Dependabot maintenance entries, as described in [Cutting a maintenance branch](../CONTRIBUTING.md#cutting-a-maintenance-branch).

2. In that branch's `docs/`, add a **`version-config.json`** with the Starlight sidebar for the version (slugs relative to the version, e.g. `"configuration"`, not `"X.Y/…"`). Leave the markdown as plain GitHub docs — **no `slug:` frontmatter** (the build injects it). Fix any links the validator later rejects (repo-root links like `/docs/crds.md#x` become relative `../crds.md#x`), then commit and push.

3. Register the version in [`versions.mjs`](./versions.mjs):

   ```js
   export const versions = [
     { slug: 'X.Y', label: 'vX.Y', ref: 'X.Y.x' },
     { slug: '2.3', label: 'v2.3', ref: '2.3.x' },
   ];
   ```

4. `npm run build` (with the branch fetched locally) — the link validator checks every version's pages. To update a published version later, commit the doc fix to its maintenance branch and redeploy; no re-tagging needed.
