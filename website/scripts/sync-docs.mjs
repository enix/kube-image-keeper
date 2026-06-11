// Populates src/content/docs (the Starlight collection dir, gitignored) from:
//   1. the repo-root ../docs tree  (the single source of truth, also rendered
//      on GitHub)
//   2. src/content/overlay         (website-only files that must not live in
//      ../docs, e.g. the homepage and the use-cases landing page)
//
// Overlay is copied last, so it wins on path conflicts. Invoked from
// astro.config.mjs (so it runs for every astro command, including CI) and
// available as `npm run sync-docs`.
import { cpSync, rmSync, mkdirSync, existsSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { argv } from 'node:process';

export function syncDocs() {
  const websiteRoot = new URL('../', import.meta.url);
  const dest = fileURLToPath(new URL('src/content/docs/', websiteRoot));
  const docs = fileURLToPath(new URL('../docs/', websiteRoot));
  const overlay = fileURLToPath(new URL('src/content/overlay/', websiteRoot));

  rmSync(dest, { recursive: true, force: true });
  mkdirSync(dest, { recursive: true });
  cpSync(docs, dest, { recursive: true });
  if (existsSync(overlay)) cpSync(overlay, dest, { recursive: true });

  console.log(`[sync-docs] copied ../docs + overlay -> ${dest}`);
}

// Run directly: `node scripts/sync-docs.mjs`
if (import.meta.url === pathToFileURL(argv[1]).href) syncDocs();
