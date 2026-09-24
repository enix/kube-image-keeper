---
paths:
  - "docs/**"
  - "website/**"
  - "README.md"
---

# Documentation

User documentation lives under `docs/` and is published from `main` at
[kuik.enix.io](https://kuik.enix.io) by `.github/workflows/website.yaml`: a broken page
ships as soon as it is merged. The markdown is the single source of truth; read it
alongside the code. Today: `docs/crds.md` (CRD reference, kept in step with
`api/kuik/v1alpha1`), `docs/configuration.md`, `docs/guides/development.md` (local
workflow) and the use cases. The v2 user docs are served from the `2.3.x` branch, not
from here.

`docs/v3/` (the design documents) is listed in `UNPUBLISHED_DOCS` of
`website/scripts/sync-docs.mjs` and renders on GitHub only. `notes/` is never published.

## Markdown conventions

The same files render on GitHub and on the Astro Starlight site. Write for GitHub first;
the build adapts:

- The page title is a leading `# H1`, never a frontmatter `title:`; the build lifts it
  into the frontmatter Starlight needs and strips it from the body. Add a frontmatter
  `description:`: it is the SEO description and the text of the use-case cards.
- Links between pages are relative markdown links with the `.md` extension
  (`./crds.md#imagemirror`); the build rewrites them to site routes. Never write a site
  route like `/crds/`: it breaks on GitHub. markdownlint checks that targets and anchors
  exist.
- Callouts use GitHub alerts (`> [!NOTE]`, `> [!TIP]`, `> [!WARNING]`, `> [!IMPORTANT]`,
  `> [!CAUTION]`); the build converts them to Starlight asides. Never use Starlight's
  `:::note`, it renders as raw text on GitHub.
- A new file under `docs/use-cases/` is picked up by the use-cases index and the sidebar
  automatically.

## How the site is built

Starlight only reads `website/src/content/docs/`, so a `sync-docs` integration
(`website/astro.config.mjs`) generates it (gitignored) before content loads: it copies
`docs/`, then `website/src/content/overlay/` (website-only pages, copied last so they
win), lifts the H1 titles and skips `UNPUBLISHED_DOCS`. Two plugins bridge the syntaxes:
`remark-github-admonitions-to-directives` for alerts and
`astro-rehype-relative-markdown-links` for links. Archived versions come from
`website/versions.mjs`: each one is sourced with `git archive` from its maintenance branch
(`2.3.x`...), whose `docs/` tree holds its markdown and sidebar, with `slug:` injected on
the fly. The full workflow is in `website/README.md` (Documentation versioning).

Local preview: `cd website && npm install && npm run dev` (Node.js 24). A watcher mirrors
`docs/` edits into the generated directory. Run one `astro dev` at a time; editing
`astro.config.mjs` or `sync-docs.mjs` restarts it.

The `astro-docs` MCP server (`.mcp.json`) searches the Astro and Starlight documentation:
use it before changing `astro.config.mjs`, the sidebar or a Starlight component, instead of
guessing an option from memory.
