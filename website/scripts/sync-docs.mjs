// Populates the gitignored Starlight collection dirs before content collections
// load. Two collections are generated:
//
//   src/content/docs/        the rendered pages, from:
//     1. the repo-root ../docs tree  (the current/in-development version, also
//        rendered on GitHub, served at the site root)
//     2. each archived version's docs, fetched with `git archive <ref> docs`
//        and laid down under src/content/docs/<slug>/ (see ../versions.mjs)
//     3. src/content/overlay  (website-only files that must not live in ../docs,
//        e.g. the use-cases landing page) — copied into the current version AND
//        into every archived version
//   src/content/versions/    the per-version Starlight sidebar configs, taken
//                            from each version's docs/version-config.json.
//
// Overlay is copied last, so it wins on path conflicts. The versioned subtrees
// only ever add /<slug>/ paths, which never collide with ../docs or overlay.
//
// Versioned docs are authored as plain GitHub markdown (no `slug:` frontmatter).
// Astro's loader and the relative-link rewriter both github-slugify the path,
// which would turn `2.2` into `22` and break routes/links — so we inject an
// explicit `slug: <slug>/<path>` into each versioned page here, on disk, before
// the collection loads (both systems read frontmatter `slug`).
//
// Invoked from astro.config.mjs (full sync at config:setup, incremental on dev
// watch for the current docs) and available as `npm run sync-docs`.
import { cpSync, rmSync, mkdirSync, existsSync, readFileSync, writeFileSync, readdirSync, statSync, mkdtempSync } from 'node:fs';
import { execSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';
import { argv } from 'node:process';
import path from 'node:path';
import { versions } from '../versions.mjs';

const websiteRoot = fileURLToPath(new URL('../', import.meta.url));
const repoRoot = path.resolve(websiteRoot, '..');
export const DEST_ROOT = path.join(websiteRoot, 'src/content/docs');
const VERSIONS_DEST = path.join(websiteRoot, 'src/content/versions');
// Watched source roots for the current version (overlay last → wins conflicts).
// Versioned docs are NOT watched: they come from git and are stable per build.
export const SOURCE_ROOTS = [
  path.resolve(websiteRoot, '../docs'),
  path.join(websiteRoot, 'src/content/overlay'),
];
const OVERLAY_ROOT = SOURCE_ROOTS[SOURCE_ROOTS.length - 1];
// Per-version config file shipped inside each version's docs/ tree; lifted into
// the `versions` collection rather than rendered as a page.
const VERSION_CONFIG_FILE = 'version-config.json';

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

// Inject `slug: <slug>` into a page's frontmatter unless it already defines one.
function injectSlug(content, slug) {
  const lines = content.split('\n');
  if (lines[0] === '---') {
    const end = lines.indexOf('---', 1);
    if (end > 0) {
      const fmLines = lines.slice(1, end);
      if (fmLines.some((l) => /^slug\s*:/.test(l))) return content;
      return ['---', `slug: ${slug}`, ...fmLines, '---', ...lines.slice(end + 1)].join('\n');
    }
  }
  return `---\nslug: ${slug}\n---\n\n${content}`;
}

// Give every versioned page an explicit slug derived from its path under the
// version dir: `<versionSlug>/<relative-path-without-extension>` (and just
// `<versionSlug>` for the version's index page).
function injectSlugsUnder(dir, versionSlug, baseDir = dir) {
  for (const entry of readdirSync(dir)) {
    const p = path.join(dir, entry);
    if (statSync(p).isDirectory()) {
      injectSlugsUnder(p, versionSlug, baseDir);
      continue;
    }
    if (!/\.mdx?$/.test(p)) continue;
    const rel = path.relative(baseDir, p).replace(/\.mdx?$/, '');
    const clean = rel.replace(/(^|\/)index$/, ''); // a directory's index page → the directory slug
    const slug = clean ? `${versionSlug}/${clean}` : versionSlug;
    const content = readFileSync(p, 'utf8');
    const injected = injectSlug(content, slug);
    if (injected !== content) writeFileSync(p, injected);
  }
}

// Resolve a configured version `ref` (a branch) to a tree-ish in this checkout.
// Local dev has it as a local branch (refs/heads/<ref>); CI checkouts have only
// the remote-tracking branch (refs/remotes/origin/<ref>). We match those branch
// refs explicitly — and only fall back to the bare ref last — so a same-named
// tag never shadows the branch (git resolves a bare name to a tag first).
function resolveRef(ref) {
  for (const candidate of [`refs/heads/${ref}`, `refs/remotes/origin/${ref}`, ref]) {
    try {
      execSync(`git -C "${repoRoot}" rev-parse --verify --quiet "${candidate}^{commit}"`, { stdio: 'ignore' });
      return candidate;
    } catch {
      // try next candidate
    }
  }
  throw new Error(
    `[sync-docs] cannot resolve git ref '${ref}' for a documentation version. ` +
    `Ensure the branch exists and the checkout fetched it (CI: actions/checkout with fetch-depth: 0).`,
  );
}

// Lay down one archived version under DEST_ROOT/<slug>/ from its git ref, and
// write its sidebar config to the `versions` collection.
function syncVersion({ slug, ref }) {
  const resolved = resolveRef(ref);
  const tmp = mkdtempSync(path.join(tmpdir(), 'kuik-docs-'));
  try {
    // `git archive <ref> docs` emits the tree rooted at `docs/`.
    execSync(`git -C "${repoRoot}" archive "${resolved}" docs | tar -x -C "${tmp}"`, { stdio: ['ignore', 'ignore', 'inherit'] });
    const srcDocs = path.join(tmp, 'docs');
    if (!existsSync(srcDocs)) {
      throw new Error(`[sync-docs] ref '${resolved}' has no docs/ directory`);
    }

    // Lift the version's sidebar config into the `versions` collection.
    const configPath = path.join(srcDocs, VERSION_CONFIG_FILE);
    if (existsSync(configPath)) {
      mkdirSync(VERSIONS_DEST, { recursive: true });
      cpSync(configPath, path.join(VERSIONS_DEST, `${slug}.json`));
      rmSync(configPath);
    } else {
      throw new Error(`[sync-docs] ref '${resolved}' is missing docs/${VERSION_CONFIG_FILE}`);
    }

    const dest = path.join(DEST_ROOT, slug);
    mkdirSync(dest, { recursive: true });
    cpSync(srcDocs, dest, { recursive: true });
    // Website-only overlay pages belong to every version too (overlay wins on
    // conflict, as it does at the site root).
    if (existsSync(OVERLAY_ROOT)) cpSync(OVERLAY_ROOT, dest, { recursive: true });
    injectSlugsUnder(dest, slug);
    return resolved;
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

export function syncDocs() {
  rmSync(DEST_ROOT, { recursive: true, force: true });
  rmSync(VERSIONS_DEST, { recursive: true, force: true });
  mkdirSync(DEST_ROOT, { recursive: true });

  // Current version (../docs + overlay).
  for (const root of SOURCE_ROOTS) {
    if (existsSync(root)) cpSync(root, DEST_ROOT, { recursive: true });
  }
  // Archived versions (one git ref each).
  const built = versions.map((v) => `${v.slug} (${syncVersion(v)})`);

  liftTitlesUnder(DEST_ROOT);
  console.log(
    `[sync-docs] synced current docs + ${versions.length} version(s) [${built.join(', ')}] -> ${DEST_ROOT}`,
  );
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
// was handled), false otherwise. Only the current-version sources are watched;
// archived versions come from git and are synced once per build.
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
