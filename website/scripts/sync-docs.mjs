// Populates src/content/docs (the Starlight collection dir, gitignored) from:
//   1. the repo-root ../docs tree  (the single source of truth, also rendered
//      on GitHub)
//   2. src/content/overlay         (website-only files that must not live in
//      ../docs, e.g. the homepage and the use-cases landing page)
//
// Overlay is copied last, so it wins on path conflicts. Invoked from
// astro.config.mjs (full sync at config:setup, incremental on dev watch) and
// available as `npm run sync-docs`.
import { cpSync, rmSync, mkdirSync, existsSync, readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs';
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

// Starlight requires the page title in frontmatter (`title:`) and would render
// a duplicate heading if the body also opened with an H1. We author docs the
// GitHub way instead — a leading `# Title` H1, no `title:` in frontmatter — and
// lift that H1 into the frontmatter here, stripping it from the body. Files that
// already carry a frontmatter title (e.g. the website-only overlay pages) are
// left untouched.
function liftTitle(content) {
  const lines = content.split('\n');
  let fmEnd = -1;
  if (lines[0] === '---') {
    for (let i = 1; i < lines.length; i++) {
      if (lines[i] === '---') { fmEnd = i; break; }
    }
  }
  const hasFrontmatter = fmEnd > 0;
  const fmLines = hasFrontmatter ? lines.slice(1, fmEnd) : [];
  if (fmLines.some((l) => /^title\s*:/.test(l))) return content;

  const bodyLines = hasFrontmatter ? lines.slice(fmEnd + 1) : lines.slice();
  // The title must be the first non-blank body line; anything else is left alone.
  let i = 0;
  while (i < bodyLines.length && bodyLines[i].trim() === '') i++;
  const m = i < bodyLines.length ? /^#\s+(.+?)\s*$/.exec(bodyLines[i]) : null;
  if (!m) return content;

  bodyLines.splice(0, i + 1); // drop leading blanks + the H1
  while (bodyLines.length && bodyLines[0].trim() === '') bodyLines.shift();

  const titleLine = `title: "${m[1].replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
  return ['---', titleLine, ...fmLines, '---', '', ...bodyLines].join('\n');
}

// Apply liftTitle in place to every .md/.mdx file under p (file or directory).
function liftTitlesUnder(p) {
  const st = statSync(p);
  if (st.isDirectory()) {
    for (const entry of readdirSync(p)) liftTitlesUnder(path.join(p, entry));
    return;
  }
  if (!/\.mdx?$/.test(p)) return;
  const content = readFileSync(p, 'utf8');
  const lifted = liftTitle(content);
  if (lifted !== content) writeFileSync(p, lifted);
}

export function syncDocs() {
  rmSync(DEST_ROOT, { recursive: true, force: true });
  mkdirSync(DEST_ROOT, { recursive: true });
  for (const root of SOURCE_ROOTS) {
    if (existsSync(root)) cpSync(root, DEST_ROOT, { recursive: true });
  }
  liftTitlesUnder(DEST_ROOT);
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
    // If overlay content (file or directory) is removed but ../docs still
    // defines the same path, fall back to the ../docs version.
    if (m.root === OVERLAY_ROOT) {
      const fallback = path.join(SOURCE_ROOTS[0], m.rel);
      if (existsSync(fallback)) {
        cpSync(fallback, m.dest, { recursive: true });
        liftTitlesUnder(m.dest);
      }
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
  liftTitlesUnder(m.dest);
  return true;
}

// Run directly: `node scripts/sync-docs.mjs`
if (import.meta.url === pathToFileURL(argv[1]).href) syncDocs();
