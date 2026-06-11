// Populates src/content/docs (the Starlight collection dir, gitignored) from:
//   1. the repo-root ../docs tree  (the single source of truth, also rendered
//      on GitHub)
//   2. src/content/overlay         (website-only files that must not live in
//      ../docs, e.g. the homepage and the use-cases landing page)
//
// Overlay is copied last, so it wins on path conflicts. Invoked from
// astro.config.mjs (full sync at config:setup, incremental on dev watch) and
// available as `npm run sync-docs`.
import { cpSync, rmSync, mkdirSync, existsSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { argv } from 'node:process';
import path from 'node:path';

const websiteRoot = fileURLToPath(new URL('../', import.meta.url));
export const DEST_ROOT = path.join(websiteRoot, 'src/content/docs');
// Order matters: later roots win on path conflicts (overlay shadows ../docs).
export const SOURCE_ROOTS = [
  path.resolve(websiteRoot, '../docs'),
  path.join(websiteRoot, 'src/content/overlay'),
];
const OVERLAY_ROOT = SOURCE_ROOTS[SOURCE_ROOTS.length - 1];

export function syncDocs() {
  rmSync(DEST_ROOT, { recursive: true, force: true });
  mkdirSync(DEST_ROOT, { recursive: true });
  for (const root of SOURCE_ROOTS) {
    if (existsSync(root)) cpSync(root, DEST_ROOT, { recursive: true });
  }
  console.log(`[sync-docs] synced ${SOURCE_ROOTS.length} source(s) -> ${DEST_ROOT}`);
}

// Map an absolute source-file path to its destination, or null if unrelated.
function destForSource(file) {
  const abs = path.resolve(file);
  for (const root of SOURCE_ROOTS) {
    const rel = path.relative(root, abs);
    if (rel && !rel.startsWith('..') && !path.isAbsolute(rel)) {
      return { root, rel, dest: path.join(DEST_ROOT, rel) };
    }
  }
  return null;
}

// Apply a single chokidar event from a source root to DEST_ROOT.
// Returns true if the event touched a watched source (so the caller knows it
// was handled), false otherwise.
export function applySourceChange(event, file) {
  const m = destForSource(file);
  if (!m) return false;

  if (event === 'unlink' || event === 'unlinkDir') {
    rmSync(m.dest, { recursive: true, force: true });
    // If an overlay file is removed but ../docs still defines the same path,
    // fall back to the ../docs version.
    if (event === 'unlink' && m.root === OVERLAY_ROOT) {
      const fallback = path.join(SOURCE_ROOTS[0], m.rel);
      if (existsSync(fallback)) cpSync(fallback, m.dest);
    }
    return true;
  }

  // add / change: respect overlay-wins — don't let a ../docs change overwrite
  // a path that the overlay is shadowing.
  if (m.root !== OVERLAY_ROOT && existsSync(path.join(OVERLAY_ROOT, m.rel))) {
    return true;
  }
  mkdirSync(path.dirname(m.dest), { recursive: true });
  cpSync(file, m.dest, { recursive: true });
  return true;
}

// Run directly: `node scripts/sync-docs.mjs`
if (import.meta.url === pathToFileURL(argv[1]).href) syncDocs();
