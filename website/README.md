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

The site serves several documentation versions through the [`starlight-versions`](https://starlight-versions.vercel.app) plugin. They are declared once in [`versions.mjs`](./versions.mjs): [`astro.config.mjs`](./astro.config.mjs) feeds the labels and slugs to the plugin, [`scripts/sync-docs.mjs`](./scripts/sync-docs.mjs) sources the pages, and the components pick the note of a page from the `kind` of its version.

| URL | What it documents | Built from | Note on its pages |
| :--- | :--- | :--- | :--- |
| `/` (`/crds/`, ...) | The latest stable line, labelled `v3.0` | `current.ref` of `versions.mjs` | None |
| `/next/` | The development version, not released yet; betas are cut from it | The working tree (`main` in CI) | Orange: not released yet |
| `/2.3/` | An older line, maintained for existing installs | The `2.3.x` branch | Blue: maintained for existing installs |
| `/X.Y/` (none yet) | A minor a later one replaced | The `X.Y.x` branch | Blue: the latest version is the root |

Every version takes its pages and its sidebar from the `docs/` tree of its `ref`: a **maintenance branch** (`2.3.x`, `3.0.x`), read with `git archive`, or the working tree when the `ref` is `null`. There is no committed copy of another version's docs in this branch. A maintenance branch also carries the code of its line, so a doc fix for a published version is an ordinary commit there, online at the push, with no release.

> [!NOTE]
> Until the `3.0.x` branch is on origin with `docs/version-config.json`, `current.ref` is `null`: the root is built from the working tree, the same pages as `/next/`. See [Switch the root to the stable branch](#switch-the-root-to-the-stable-branch).

What follows from serving the stable line at the root:

- **Unversioned URLs are a public interface.** The published chart (`NOTES.txt`, the dashboard, the Prometheus rules), the README and the release notes link to root pages (`/known-limits/`, `/observability/#alerts`, `/concepts/pacing/`). Never rename or remove a page or a heading they point to without a redirect (`redirects` in `astro.config.mjs`).
- **A doc fix for a published version** is a `docs:` commit on `main`, where it shows under `/next/`, then a cherry-pick onto `X.Y.x` through a pull request, where it shows at the root (or under `/X.Y/`) at the merge. A fix that only holds for the line goes to its branch directly. Without the cherry-pick the root keeps the mistake.
- **Release notes**: those of a beta that cite a page only `main` has link to `/next/...`; those of a stable release link to the root.
- **A change of `website/`** (theme, components, `versions.mjs`) on `main` applies to every version: the deployed site is always built with `main`'s `website/`. A maintenance branch keeps its own copy, used only by the build of a pull request that targets it.

### How the pages are generated

At build time, `sync-docs` generates the gitignored collection dirs:

- For the root and for each version it copies the `docs/` tree of the `ref` into `src/content/docs/` (the root) or `src/content/docs/<slug>/`. The docs are plain GitHub markdown with **no `slug:` frontmatter**; `sync-docs` injects `slug: <slug>/<path>` into each page of a version under a slug, because Astro's loader and the relative-link rewriter both github-slugify the path, which would turn `2.3` into `23` and break routes and links. It also prefixes the absolute links of the frontmatter (the hero buttons of the home page, `link: /getting-started/`) with the slug.
- Each tree carries `docs/version-config.json`, the Starlight sidebar of its version, never rendered as a page. The root sidebar is read from the file of the root's tree; the one of a version under a slug goes to `src/content/versions/<slug>.json` (the `versions` content collection, see [`src/content.config.ts`](./src/content.config.ts)). Its slugs are relative to the version (`"configuration"`, not `"2.3/..."`); the plugin prepends the version slug. A tree without the file fails the build with a message that names it.
- Subtrees of `docs/` listed in `UNPUBLISHED_DOCS` (in `sync-docs.mjs`, currently `v3/`, the v3 design documents) are **not** copied at all, from the working tree, from a maintenance branch or by the dev watcher. They render on GitHub only: they are not user documentation and they link to non-page files (YAML examples, bare directories) that the site cannot serve and the links validator would reject.
- The website-only [`src/content/overlay/`](./src/content/overlay/) pages (currently the use-cases landing `use-cases/index.mdx`) are copied into the root and into **every** version, from the working tree. The home page is a normal `docs/index.md` page, so each version has its own.

Because every configured version already has its docs on disk at build time, the plugin never triggers its built-in "archive the current docs" behaviour. The build needs the maintenance branches: a local branch or its `origin/` counterpart locally, and `fetch-depth: 0` in CI (see [`.github/workflows/website.yaml`](../.github/workflows/website.yaml)). A branch that is missing fails the build with a message that names it.

In `npm run dev`, a watcher mirrors each edit of `../docs` into the targets built from the working tree (`/next/`, and the root while `current.ref` is `null`), with the same processing as the full sync. A change of the root sidebar needs a restart, since it is part of the Astro config.

### When the site is rebuilt

[`website.yaml`](../.github/workflows/website.yaml) builds and deploys on a push to `main` or to a maintenance branch `X.Y.x` that touches `docs/` or `website/`, and on a manual run (`workflow_dispatch`). It always checks `main` out to build. Nothing is built from a tag. A pull request only builds, which validates its links.

On a push to `X.Y.x`, GitHub runs the `website.yaml` of that branch, not `main`'s: bring a change of the workflow to the maintenance branches too.

### Version notes

Root pages carry no note. On the pages of a version under a slug, [`src/components/PageTitle.astro`](./src/components/PageTitle.astro) replaces the plugin's "outdated" notice with a note under the page title, and [`src/components/Banner.astro`](./src/components/Banner.astro) shows the same note as a banner on the landing pages, which `PageTitle` skips, and suppresses the plugin's splash banner. The sentence comes from [`src/components/VersionNoteText.astro`](./src/components/VersionNoteText.astro), by the `kind` of the version in `versions.mjs`:

- `unreleased` (`/next/`), orange: the page documents the development version, not released yet, and the latest release is linked.
- `maintained` (`/2.3/`), blue: the version is maintained for existing installs, new installs use the latest release, and [Upgrading from v2](../docs/installation.md#upgrading-from-v2) is linked.
- `superseded` (`/X.Y/`), blue: the page documents `vX.Y`, and the latest version is linked.

Each note links to the same page at the root, or to the root home when the page has no counterpart there.

The `unreleased` pages also carry `<meta name="robots" content="noindex">` ([`src/components/Head.astro`](./src/components/Head.astro)) and stay out of the sitemap (the `sitemap` filter of `astro.config.mjs`): a reader who searches for kuik lands on the docs of a release. The search box is already limited to the version being read.

Overriding `PageTitle` makes the plugin log a one-line "already defined" warning at build: expected, since we render the note ourselves instead of the plugin's `<VersionNotice />`.

[`src/content/i18n/en.json`](./src/content/i18n/en.json) rewords the plugin's **search**-modal strings (still rendered by the plugin) on the pages of a version, so that they point to the root docs.

### Switch the root to the stable branch

Once, for `3.0.x`:

1. Create and push `3.0.x` as described in [Cutting a maintenance branch](../CONTRIBUTING.md#cutting-a-maintenance-branch).
2. Bring `docs/version-config.json` to that branch, with a pull request that targets it: cherry-pick the commits of `main` that added the file and changed `website/` and `website.yaml`. A branch cut from `main` after them already has everything.
3. On `main`, change one line of [`versions.mjs`](./versions.mjs), then `npm run build` with the branch fetched:

   ```js
   export const current = { label: 'v3.0', ref: '3.0.x' };
   ```

   In the same commit, drop the note above that says the root is still built from the working tree, and the parenthesis of [Documentation of a released version](../CONTRIBUTING.md#documentation-of-a-released-version) that says the same.

From that merge on, the root only changes with `3.0.x`, and `/next/` with `main`.

### When a new minor is released

When `X.Y.0` is released (say 3.1.0):

1. Create and push `X.Y.x` as described in [Cutting a maintenance branch](../CONTRIBUTING.md#cutting-a-maintenance-branch). Cut from `main`, it already carries `docs/version-config.json`; keep its sidebar in step with the pages of the line.
2. On `main`, in [`versions.mjs`](./versions.mjs), point the root at the new line and turn the previous one into an archive, first in the list:

   ```js
   export const current = { label: 'v3.1', ref: '3.1.x' };

   export const versions = [
     { slug: '3.0', label: 'v3.0', ref: '3.0.x', kind: 'superseded' },
     { slug: '2.3', label: 'v2.3', ref: '2.3.x', kind: 'maintained' },
     { slug: 'next', label: 'next', ref: null, kind: 'unreleased' },
   ];
   ```

3. `npm run build` with the branches fetched: the link validator checks the pages of every version. The build fails while a branch is missing, so the order holds.

An archive is never removed: its URLs are public.
