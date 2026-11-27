// Which documentation version a page belongs to. The components use it to pick
// the note of a page, astro.config.mjs to keep the unreleased pages out of the
// sitemap.
import { versions } from '../../versions.mjs';

// The entry of versions.mjs a page id (`2.3/configuration`) or a URL path
// (`/2.3/configuration/`) belongs to, or undefined for a page of the site root.
export function versionOf(idOrPath) {
  const [slug] = idOrPath.replace(/^\//, '').split('/');
  return versions.find((version) => version.slug === slug);
}

// True for a page of the development version, not released yet.
export function isUnreleased(idOrPath) {
  return versionOf(idOrPath)?.kind === 'unreleased';
}
