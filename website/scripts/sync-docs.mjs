// Populates the gitignored Starlight collection dirs before content collections
// load. Two collections are generated:
//
//   src/content/docs/        the rendered pages. The site root and each version
//                            under <slug>/ (see ../versions.mjs) get:
//     1. the docs/ tree of their `ref`: a maintenance branch, fetched with
//        `git archive <ref> docs`, or the working tree (../docs, `main` in CI)
//        when the ref is null
//     2. src/content/overlay  (website-only files that must not live in ../docs,
//        e.g. the use-cases landing page), from the working tree for everyone
//   src/content/versions/    the Starlight sidebar of each version under a
//                            slug, taken from the docs/version-config.json of
//                            its tree. The root sidebar comes from the same
//                            file of the root's tree, through currentSidebar().
//
// Overlay is copied last, so it wins on path conflicts.
//
// The docs are authored as plain GitHub markdown (no `slug:` frontmatter).
// Astro's loader and the relative-link rewriter both github-slugify the path,
// which would turn `x.y` into `xy` and break routes/links, so we inject an
// explicit `slug: <slug>/<path>` into each page of a version under a slug here,
// on disk, before the collection loads (both systems read frontmatter `slug`).
//
// Invoked from astro.config.mjs (full sync at config:setup, incremental on dev
// watch for the working tree) and available as `npm run sync-docs`.
import { cpSync, rmSync, mkdirSync, existsSync, readFileSync, writeFileSync, readdirSync, statSync, mkdtempSync } from 'node:fs';
import { execSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';
import { argv } from 'node:process';
import path from 'node:path';
import { current, versions } from '../versions.mjs';

const websiteRoot = fileURLToPath(new URL('../', import.meta.url));
const repoRoot = path.resolve(websiteRoot, '..');
export const DEST_ROOT = path.join(websiteRoot, 'src/content/docs');
const VERSIONS_DEST = path.join(websiteRoot, 'src/content/versions');
// Watched source roots (overlay last → wins conflicts). Trees that come from
// git are NOT watched: they are stable per build.
export const SOURCE_ROOTS = [
  path.resolve(websiteRoot, '../docs'),
  path.join(websiteRoot, 'src/content/overlay'),
];
const OVERLAY_ROOT = SOURCE_ROOTS[SOURCE_ROOTS.length - 1];
const DOCS_ROOT = SOURCE_ROOTS[0];
// Subtrees of a docs/ tree that stay GitHub-only and are never published on
// the site: design documents that are not user documentation (they link to
// non-page files such as YAML examples, which the site cannot serve). Relative
// to docs/; nothing under them is copied, from the working tree, from a
// maintenance branch or by the dev watcher.
export const UNPUBLISHED_DOCS = ['v3'];
// Sidebar of a version, shipped inside its docs/ tree; never rendered as a page.
const VERSION_CONFIG_FILE = 'version-config.json';

// Everything the site serves: the root (no slug) first, then each version under
// its slug. A target with a null ref is sourced from the working tree.
const TARGETS = [{ slug: null, ref: current.ref }, ...versions.map(({ slug, ref }) => ({ slug, ref }))];

// The generated directory of a target.
function destOf(slug) {
  return slug ? path.join(DEST_ROOT, slug) : DEST_ROOT;
}

// True when `rel` (a path relative to a docs/ tree) falls under an unpublished
// subtree. The overlay has none.
function isUnpublished(rel) {
  return UNPUBLISHED_DOCS.some((dir) => rel === dir || rel.startsWith(dir + path.sep));
}

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

// Append a directory's index file to relative *directory* links (`](./foo/)`,
// optionally with a `#anchor`), so they resolve and validate on the site. Such
// links are authored bare so they also work on GitHub (folder view) — the
// website-only landing pages they point at (e.g. use-cases/index.mdx) don't
// exist in ../docs, so a `.mdx` link would 404 on GitHub. Runs after all copies,
// when the target index files are present.
function postfixDirIndexLinks(filePath) {
  if (!/\.mdx?$/.test(filePath)) return;
  const content = readFileSync(filePath, 'utf8');
  const fileDir = path.dirname(filePath);
  const rewritten = content.replace(
    /(\]\()(\.\.?\/[^)\s#]*?\/)(#[^)]*)?(\))/g,
    (match, open, rel, anchor = '', close) => {
      const targetDir = path.resolve(fileDir, rel);
      for (const index of ['index.mdx', 'index.md']) {
        if (existsSync(path.join(targetDir, index))) return `${open}${rel}${index}${anchor}${close}`;
      }
      return match; // no index page in that directory — leave the link as-is
    },
  );
  if (rewritten !== content) writeFileSync(filePath, rewritten);
}

// Apply postfixDirIndexLinks to every .md/.mdx file under p (file or directory).
function postfixDirIndexLinksUnder(p) {
  const st = statSync(p);
  if (st.isDirectory()) {
    for (const entry of readdirSync(p)) postfixDirIndexLinksUnder(path.join(p, entry));
    return;
  }
  postfixDirIndexLinks(p);
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

// Prefix the absolute site links of a page's frontmatter with the version slug
// (the hero actions of the home page, `link: /getting-started/`), so the home of
// a version leads to that version's pages. Body links are relative and follow
// the injected slug on their own. A link already under the slug is left alone.
function prefixFrontmatterLinks(content, slug) {
  const lines = content.split('\n');
  if (lines[0] !== '---') return content;
  const end = lines.indexOf('---', 1);
  if (end < 0) return content;
  for (let i = 1; i < end; i++) {
    lines[i] = lines[i].replace(/^(\s*link:\s*["']?)(\/[^\s"']*)/, (match, key, link) =>
      link === `/${slug}` || link.startsWith(`/${slug}/`) ? match : `${key}/${slug}${link}`,
    );
  }
  return lines.join('\n');
}

// Root every page under p (file or directory) in its version: an explicit slug
// derived from its path under the version dir,
// `<versionSlug>/<relative-path-without-extension>` (just `<versionSlug>` for
// the version's index page), and frontmatter links under that slug.
function versionPagesUnder(p, versionSlug) {
  if (statSync(p).isDirectory()) {
    for (const entry of readdirSync(p)) versionPagesUnder(path.join(p, entry), versionSlug);
    return;
  }
  if (!/\.mdx?$/.test(p)) return;
  const rel = path.relative(destOf(versionSlug), p).replace(/\.mdx?$/, '');
  const clean = rel.replace(/(^|\/)index$/, ''); // a directory's index page → the directory slug
  const slug = clean ? `${versionSlug}/${clean}` : versionSlug;
  const content = readFileSync(p, 'utf8');
  const versioned = prefixFrontmatterLinks(injectSlug(content, slug), versionSlug);
  if (versioned !== content) writeFileSync(p, versioned);
}

// Turn the pages under p (file or directory), copied as authored into the
// generated directory of a target, into what Starlight reads. Runs once the
// overlay is in place: the directory links resolve against it.
function processPages(p, slug) {
  if (slug) versionPagesUnder(p, slug);
  liftTitlesUnder(p);
  postfixDirIndexLinksUnder(p);
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

// Lay down one target (the root, or a version under DEST_ROOT/<slug>/) from the
// docs/ tree of its ref, and return the name of that tree.
function syncTarget({ slug, ref }) {
  return withDocsTree(ref, (srcDocs, origin) => {
    if (!existsSync(path.join(srcDocs, VERSION_CONFIG_FILE))) {
      throw new Error(`[sync-docs] ${origin} is missing docs/${VERSION_CONFIG_FILE}`);
    }
    const dest = destOf(slug);
    mkdirSync(dest, { recursive: true });
    cpSync(srcDocs, dest, {
      recursive: true,
      filter: (src) => {
        const rel = path.relative(srcDocs, src);
        return rel !== VERSION_CONFIG_FILE && !isUnpublished(rel);
      },
    });
    // Website-only overlay pages belong to every target (overlay wins on conflict).
    if (existsSync(OVERLAY_ROOT)) cpSync(OVERLAY_ROOT, dest, { recursive: true });
    // The root sidebar goes through currentSidebar(), a version's through the
    // `versions` collection.
    if (slug) writeVersionConfig(slug, readFileSync(path.join(srcDocs, VERSION_CONFIG_FILE), 'utf8'));
    processPages(dest, slug);
    return origin;
  });
}

// Run fn on the docs/ tree of a ref and on a name for it in messages: the
// working tree when the ref is null, else the tree of the branch, extracted to a
// temporary directory for the time of the call.
function withDocsTree(ref, fn) {
  if (ref === null) return fn(DOCS_ROOT, 'the working tree');
  const resolved = resolveRef(ref);
  const tmp = mkdtempSync(path.join(tmpdir(), 'kuik-docs-'));
  try {
    // `git archive <ref> docs` emits the tree rooted at `docs/`.
    execSync(`git -C "${repoRoot}" archive "${resolved}" docs | tar -x -C "${tmp}"`, { stdio: ['ignore', 'ignore', 'inherit'] });
    const srcDocs = path.join(tmp, 'docs');
    if (!existsSync(srcDocs)) {
      throw new Error(`[sync-docs] ref '${resolved}' has no docs/ directory`);
    }
    return fn(srcDocs, `ref '${resolved}'`);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

// Write the sidebar config of a version into the `versions` collection.
function writeVersionConfig(slug, json) {
  mkdirSync(VERSIONS_DEST, { recursive: true });
  writeFileSync(path.join(VERSIONS_DEST, `${slug}.json`), json);
}

// The Starlight sidebar of the site root: the `sidebar` of the
// docs/version-config.json of the root's tree. astro.config.mjs needs it while
// the config is evaluated, before the sync runs, so it is read on its own here.
export function currentSidebar() {
  let json;
  if (current.ref === null) {
    const configPath = path.join(DOCS_ROOT, VERSION_CONFIG_FILE);
    if (!existsSync(configPath)) {
      throw new Error(`[sync-docs] the working tree is missing docs/${VERSION_CONFIG_FILE}`);
    }
    json = readFileSync(configPath, 'utf8');
  } else {
    const resolved = resolveRef(current.ref);
    try {
      json = execSync(`git -C "${repoRoot}" show "${resolved}:docs/${VERSION_CONFIG_FILE}"`, {
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'ignore'],
      });
    } catch {
      throw new Error(`[sync-docs] ref '${resolved}' is missing docs/${VERSION_CONFIG_FILE}`);
    }
  }
  return JSON.parse(json).sidebar;
}

export function syncDocs() {
  rmSync(DEST_ROOT, { recursive: true, force: true });
  rmSync(VERSIONS_DEST, { recursive: true, force: true });
  mkdirSync(DEST_ROOT, { recursive: true });

  // The root first: its pages must be processed before the version directories
  // appear under it.
  const built = TARGETS.map((target) => {
    const origin = syncTarget(target);
    if (!target.slug) {
      for (const { slug } of versions) {
        if (existsSync(destOf(slug))) {
          throw new Error(`[sync-docs] docs/${slug} of ${origin} collides with the version slug '${slug}'`);
        }
      }
    }
    return `${target.slug ?? 'root'} (${origin})`;
  });
  console.log(`[sync-docs] synced ${built.join(', ')} -> ${DEST_ROOT}`);
}

// Map an absolute source-file path to its root and relative path, or null if
// unrelated or unpublished.
function matchSource(file) {
  const abs = path.resolve(file);
  for (const root of SOURCE_ROOTS) {
    const rel = path.relative(root, abs);
    if (rel && !rel.startsWith('..') && !path.isAbsolute(rel)) {
      if (root === DOCS_ROOT && isUnpublished(rel)) return null;
      return { root, rel };
    }
  }
  return null;
}

// Apply a single chokidar event from a source root to the generated
// directories, with the same processing as the full sync (a page of /next/ keeps
// its injected slug). Returns true if the event touched a watched source (so the
// caller knows it was handled), false otherwise. Only the working tree is
// watched; the trees that come from git are synced once per build.
export function applySourceChange(event, file) {
  const m = matchSource(file);
  if (!m) return false;
  const removed = event === 'unlink' || event === 'unlinkDir';

  // The sidebar of the working tree: rewrite it for the versions under a slug.
  // The root sidebar is part of the Astro config, so it needs a restart.
  if (m.root === DOCS_ROOT && m.rel === VERSION_CONFIG_FILE) {
    if (removed) return true;
    for (const { slug, ref } of TARGETS) {
      if (slug && ref === null) writeVersionConfig(slug, readFileSync(file, 'utf8'));
    }
    return true;
  }

  // add / change: respect overlay-wins, don't let a ../docs change overwrite
  // a path that the overlay is shadowing.
  if (!removed && m.root !== OVERLAY_ROOT && existsSync(path.join(OVERLAY_ROOT, m.rel))) {
    return true;
  }

  // An overlay page belongs to every target, a ../docs page to the targets
  // sourced from the working tree.
  for (const { slug, ref } of TARGETS) {
    if (m.root === DOCS_ROOT && ref !== null) continue;
    const dest = path.join(destOf(slug), m.rel);
    let source = file;
    if (removed) {
      rmSync(dest, { recursive: true, force: true });
      // If overlay content (file or directory) is removed but ../docs still
      // defines the same path, fall back to the ../docs version.
      source = path.join(DOCS_ROOT, m.rel);
      if (m.root !== OVERLAY_ROOT || ref !== null || !existsSync(source)) continue;
    }
    mkdirSync(path.dirname(dest), { recursive: true });
    cpSync(source, dest, { recursive: true });
    processPages(dest, slug);
  }
  return true;
}

// Run directly: `node scripts/sync-docs.mjs`
if (import.meta.url === pathToFileURL(argv[1]).href) syncDocs();
