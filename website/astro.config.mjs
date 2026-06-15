// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import favicons from 'astro-favicons';
import remarkGithubAdmonitionsToDirectives from 'remark-github-admonitions-to-directives';
import rehypeAstroRelativeMarkdownLinks from 'astro-rehype-relative-markdown-links';
import starlightLinksValidator from 'starlight-links-validator';
import starlightVersions from 'starlight-versions';
import chokidar from 'chokidar';
import { syncDocs, applySourceChange, SOURCE_ROOTS } from './scripts/sync-docs.mjs';
import { versions } from './versions.mjs';

// Sync ../docs + overlay into src/content/docs before content collections load.
// Runs for every astro command (build, dev, CI via withastro/action), so the
// gitignored collection dir is always present and current.
const syncDocsIntegration = {
  name: 'sync-docs',
  hooks: {
    'astro:config:setup': () => syncDocs(),
    // In dev, watch the source roots with our own chokidar instance. Vite's
    // watcher won't reliably track ../docs (it lives outside the project root),
    // so we run a dedicated watcher and copy each change into src/content/docs,
    // which Astro IS watching — its content HMR then picks it up.
    'astro:server:setup': ({ server, logger }) => {
      const watcher = chokidar.watch(SOURCE_ROOTS, { ignoreInitial: true });
      watcher.on('all', (event, file) => {
        if (applySourceChange(event, file)) logger.info(`${event} ${file}`);
      });
      server.watcher.on('close', () => watcher.close());
    },
  },
};

// https://astro.build/config
export default defineConfig({
  site: 'https://kuik.enix.io',
  markdown: {
    remarkPlugins: [
      // (> [!NOTE]) into the directive syntax (:::note) it renders.
      [remarkGithubAdmonitionsToDirectives, {
        mapping: {
          NOTE: 'note',
          TIP: 'tip',
          IMPORTANT: 'note',
          WARNING: 'caution',
          CAUTION: 'danger',
        },
      }]
    ],
    // Rewrite relative .md links (./crds.md#x, ../crds.md#x) authored for GitHub
    // into site routes. collectionBase: false because Starlight serves the docs
    // collection at the site root (/crds/, not /docs/crds/); trailingSlash to
    // match Starlight's default.
    rehypePlugins: [
      [rehypeAstroRelativeMarkdownLinks, { collectionBase: false, trailingSlash: 'always' }],
    ],
  },
  integrations: [
    syncDocsIntegration,
    starlight({
      title: 'kube-image-keeper',
      description: 'Documentation for kube-image-keeper (kuik), the Kubernetes operator for container image routing, mirroring, and replication.',
      logo: {
        src: './src/assets/logo.svg',
        alt: 'kube-image-keeper logo',
        replacesTitle: true,
      },
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/enix/kube-image-keeper',
        },
      ],
      // Build-time validation of internal links and heading anchors. Runs after
      // astro-rehype-relative-markdown-links has rewritten the GitHub-style .md
      // links to site routes, so it validates the final shipped routes.
      //
      // starlight-versions serves older docs under /<slug>/. The current docs
      // live in src/content/docs (synced from ../docs); each archived version's
      // docs are fetched from a git branch by sync-docs (see ../versions.mjs)
      // into src/content/docs/<slug>/, with its sidebar from that branch's
      // docs/version-config.json written to src/content/versions/<slug>.json.
      // Because every configured version already has its docs on disk at build
      // time, the plugin never auto-archives the current docs — see
      // website/README.md for the "add a version" flow.
      plugins: [
        starlightLinksValidator(),
        starlightVersions({
          current: { label: 'main' },
          versions: versions.map(({ slug, label }) => ({ slug, label })),
        }),
      ],
      components: {
        SiteTitle: './src/components/SiteTitle.astro',
        Head: './src/components/Head.astro',
        // Three-level version notice under the page title (see the component):
        // the in-development `main` docs get a blue info note, the latest stable
        // release gets nothing, and older releases get the orange "outdated" note.
        PageTitle: './src/components/PageTitle.astro',
        // Same three-level notice as a full-width banner on landing/splash pages
        // (which PageTitle skips), and suppresses the plugin's splash banner.
        Banner: './src/components/Banner.astro',
      },
      sidebar: [
        { label: 'Home', link: '/' },
        {
          label: 'Configuration',
          items: [
            'configuration',
            'image-routing',
            'resource-filtering',
            'crds',
          ],
        },
        {
          label: 'Use cases',
          items: [{ autogenerate: { directory: 'use-cases' } }],
        },
        {
          label: 'Guides',
          items: [{ autogenerate: { directory: 'guides' } }],
        },
      ],
    }),
    favicons({
      input: {
        favicons: ['./src/assets/logo.svg']
      },
      icons: {
        favicons: true,
        appleIcon: false,
        android: false,
        appleStartup: false,
        windows: false,
        yandex: false,
      },
    }),
  ],
});
