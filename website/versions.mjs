// Single source of truth for the documentation versions the site serves.
//
// The current/in-development docs live in the repo-root ../docs tree (served at
// the site root, labelled `main`). Each archived version's docs — and its
// Starlight sidebar in docs/version-config.json — live on that version's
// maintenance branch (`ref`, e.g. `2.2.x`); the website build sources them with
// `git archive <ref> docs` at build time (see scripts/sync-docs.mjs), so there
// is no committed duplicate of the versioned docs in this branch. The same
// branch carries the release-line code, so docs and code for a version stay
// together and doc fixes ship as ordinary commits on the maintenance branch.
//
// - astro.config.mjs feeds { slug, label } to the starlight-versions plugin.
// - sync-docs.mjs uses { slug, ref } to fetch each version's docs and config.
export const versions = [
  { slug: '2.2', label: 'v2.2', ref: '2.2.x' },
];
