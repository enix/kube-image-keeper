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

- The **current** version (labelled `main` in the version picker) is the live `../docs` tree, served at the site root (`/configuration/`, `/crds/`, …).
- Each **archived** version's docs live on a dedicated **git branch** (`ref` in `versions.mjs`, e.g. `docs/v2.2`) and are served under the version slug (`/2.2/configuration/`, …). There is **no committed copy** of the versioned docs in this branch.

At build time, `sync-docs` generates the gitignored collection dirs:

- For each version it runs `git archive <ref> docs` and lays the result under `src/content/docs/<slug>/`. The branch's docs are plain GitHub markdown with **no `slug:` frontmatter**; `sync-docs` injects `slug: <slug>/<path>` into each page on disk, because Astro's loader and the relative-link rewriter both github-slugify the path — which would turn `2.2` into `22` and break routes/links.
- That branch also carries `docs/version-config.json` (the version's Starlight sidebar); `sync-docs` lifts it into `src/content/versions/<slug>.json` (the `versions` content collection, see [`src/content.config.ts`](./src/content.config.ts)) and does not render it as a page. Sidebar slugs in it are relative to the version (`"configuration"`, not `"2.2/…"`); the plugin prepends the version slug.
- The website-only [`src/content/overlay/`](./src/content/overlay/) pages (currently the use-cases landing `use-cases/index.mdx`) are copied into **every** version, not just the current one (the homepage is a normal `../docs/index.md` page, so it is per-version automatically). Note the use-cases landing's `getCollection` is not version-scoped, so each version's `/…/use-cases/` index currently lists the **current** version's use cases — the per-version use-case pages themselves are correct and in the sidebar.

Because every configured version already has its docs on disk at build time, the plugin never triggers its built-in "archive the current docs" behaviour. The build therefore needs git history for the version branches — CI's checkout uses `fetch-depth: 0` (see [`.github/workflows/website.yaml`](../.github/workflows/website.yaml)).

### Version messaging (main = in-development, archived slugs = stable)

The site root is the **in-development** `main` docs; the archived slugs are the
**stable releases**. That inverts the plugin's default assumption (current =
newest = best — it only ever flags archived versions as "outdated"), so
[`src/components/PageTitle.astro`](./src/components/PageTitle.astro) replaces the
plugin's notice with a three-level one, shown under the page title:

| Level | Pages | Notice |
| --- | --- | --- |
| **next** | the in-development `main` docs (site root) | blue _info_ note linking to the latest stable release |
| **current** | the latest stable release (newest archived version, `versions[0]`) | none |
| **previous** | any older archived release | orange _warning_ note linking to the latest stable release |

The component classifies a page by matching its id against the configured
version slugs, treats `versions[0]` as the current stable, and skips landing
pages (the homepage hero). Overriding `PageTitle` makes the plugin log a one-line
"already defined" warning at build — expected, since we render the notice
ourselves instead of the plugin's `<VersionNotice />`.

[`src/content/i18n/en.json`](./src/content/i18n/en.json) additionally rewords the
plugin's **search**-modal version strings (still rendered by the plugin) so they
don't tell stable-release readers to "switch to the latest version for up-to-date
results".

### Add a new archived version

To publish version `X.Y` (e.g. when cutting a release):

1. Create a long-lived **`docs/vX.Y` branch** whose `docs/` directory holds that version's documentation, typically branched from the release tag:

   ```bash
   git switch -c docs/vX.Y vX.Y.Z
   ```

2. In that branch's `docs/`, add a **`version-config.json`** with the Starlight sidebar for the version (slugs relative to the version, e.g. `"configuration"`, not `"X.Y/…"`). Leave the markdown as plain GitHub docs — **no `slug:` frontmatter** (the build injects it). Fix any links the validator later rejects (repo-root links like `/docs/crds.md#x` become relative `../crds.md#x`), then commit and `git push -u origin docs/vX.Y`.

3. Register the version in [`versions.mjs`](./versions.mjs):

   ```js
   export const versions = [
     { slug: 'X.Y', label: 'vX.Y', ref: 'docs/vX.Y' },
     { slug: '2.2', label: 'v2.2', ref: 'docs/v2.2' },
   ];
   ```

4. `npm run build` (with the branch fetched locally) — the link validator checks every version's pages. To update a published version later, commit to its branch and redeploy; no re-tagging needed.
