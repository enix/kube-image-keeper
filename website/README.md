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

The site serves multiple documentation versions via the [`starlight-versions`](https://starlight-versions.vercel.app) plugin, wired up in [`astro.config.mjs`](./astro.config.mjs):

- The **current** version (labelled `main` in the version picker) is the live `../docs` tree, served at the site root (`/configuration/`, `/crds/`, …). Each **archived** version is served under its slug (`/2.2/configuration/`, …).

Because `src/content/docs/` is generated and gitignored (see [`scripts/sync-docs.mjs`](./scripts/sync-docs.mjs)), an archived version cannot just live there — it would be wiped on every build. Instead each version is a committed **frozen snapshot** that `sync-docs` copies into `src/content/docs/<slug>/` on every build, alongside the current docs:

- `src/content/versioned-docs/<slug>/` — the version's markdown. Every page carries an explicit `slug:` frontmatter (`slug: 2.2/configuration`) so Astro serves it under `/<slug>/` verbatim — without it, Astro's slugifier strips the dot from `2.2` and the routes/sidebar links break.
- `src/content/versions/<slug>.json` — the version's sidebar (the `versions` content collection, see [`src/content.config.ts`](./src/content.config.ts)). Slugs in it are relative to the version (`"configuration"`, not `"2.2/…"`); the plugin prepends the version slug.

Because every configured version already has its snapshot on disk, the plugin never triggers its built-in "archive the current docs" behaviour.

### Add a new archived version

To freeze the current docs as version `X.Y` (e.g. when cutting a release):

1. Snapshot the docs at the release tag into the source tree:
   ```bash
   mkdir -p website/src/content/versioned-docs/X.Y
   git archive vX.Y.Z docs | tar -x -C website/src/content/versioned-docs/X.Y --strip-components=1
   ```
2. Add a `slug:` frontmatter to every page (`slug: X.Y/<path-without-extension>`, and `slug: X.Y` for `index.md`). Fix any links the validator rejects — repo-root links like `/docs/crds.md#x` become relative (`../crds.md#x`).
3. Create `website/src/content/versions/X.Y.json` with a sidebar matching that version's file layout (slugs relative to the version).
4. Register the version in [`astro.config.mjs`](./astro.config.mjs):
   ```js
   starlightVersions({
     current: { label: 'main' },
     versions: [{ slug: 'X.Y', label: 'vX.Y' }, { slug: '2.2', label: 'v2.2' }],
   })
   ```
5. `npm run build` — the link validator checks every version's pages.
