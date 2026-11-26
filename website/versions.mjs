// Single source of truth for the documentation versions the site serves.
//
// Every version takes its pages and its sidebar (docs/version-config.json) from
// the docs/ tree of a `ref`:
//
// - a branch name (`3.0.x`, `2.3.x`): the tree of that maintenance branch, read
//   with `git archive <ref> docs` at build time (see scripts/sync-docs.mjs). The
//   branch also carries the code of the line, so a doc fix for a published
//   version is an ordinary commit there, online at the push.
// - `null`: the working tree, that is `main` in CI.
//
// - astro.config.mjs feeds the labels and slugs to the starlight-versions plugin.
// - sync-docs.mjs uses the refs to lay the pages and the sidebars down.
// - src/lib/version.mjs uses `kind` to pick the note shown on a page.

// Served at the site root: the latest stable line. Unversioned URLs (the chart,
// the README, the release notes) land here.
//
// `ref: null` until the `3.0.x` branch is on origin with docs/version-config.json:
// the root is then the same tree as /next/. Switching the root to the stable
// line is this one line, `ref: '3.0.x'` (see website/README.md).
export const current = { label: 'v3.0', ref: null };

// Served under /<slug>/, in the order of the version picker. `kind` is one of:
//
// - `unreleased`: the development version (`main`), not released yet. Its pages
//   say so, carry `noindex` and stay out of the sitemap.
// - `maintained`: an older line still maintained for existing installs.
// - `superseded`: a minor a later one replaced, kept as an archive under `/X.Y/`.
export const versions = [
  { slug: '2.3', label: 'v2.3', ref: '2.3.x', kind: 'maintained' },
  { slug: 'next', label: 'next', ref: null, kind: 'unreleased' },
];
