// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import favicons from 'astro-favicons';
import remarkGithubAdmonitionsToDirectives from 'remark-github-admonitions-to-directives';
import rehypeAstroRelativeMarkdownLinks from 'astro-rehype-relative-markdown-links';
import starlightLinksValidator from 'starlight-links-validator';
import starlightVersions from 'starlight-versions';
import chokidar from 'chokidar';
import { syncDocs, applySourceChange, currentSidebar, SOURCE_ROOTS } from './scripts/sync-docs.mjs';
import { current, versions } from './versions.mjs';
import { isUnreleased } from './src/lib/version.mjs';

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
    // Declared here so that Starlight does not add its own: the pages of the
    // development version stay out of the sitemap (and carry noindex, see
    // src/components/Head.astro).
    sitemap({ filter: (page) => !isUnreleased(new URL(page).pathname) }),
    starlight({
      title: 'kube-image-keeper',
      description: 'Documentation for kube-image-keeper (kuik), the Kubernetes operator for container image routing, mirroring, and replication.',
      // Widen the content column beyond Starlight's narrow default (45rem).
      customCss: ['./src/styles/custom.css'],
      logo: {
        src: '../docs/assets/logo.svg',
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
      // starlight-versions serves the root (the latest stable line) and one
      // version per slug under /<slug>/. sync-docs lays each of them down in
      // src/content/docs from the docs/ tree of its ref, a maintenance branch
      // or the working tree (see ../versions.mjs), with the sidebar of a slug
      // written to src/content/versions/<slug>.json. Because every configured
      // version already has its docs on disk at build time, the plugin never
      // archives the root docs itself: see website/README.md.
      plugins: [
        starlightLinksValidator(),
        starlightVersions({
          current: { label: current.label },
          versions: versions.map(({ slug, label }) => ({ slug, label })),
        }),
      ],
      components: {
        SiteTitle: './src/components/SiteTitle.astro',
        Head: './src/components/Head.astro',
        // Version note under the page title (see the component): the root pages
        // get nothing, the pages of a version under a slug say which version
        // they document (not released yet, or an older line) and link to the
        // same page at the root.
        PageTitle: './src/components/PageTitle.astro',
        // Same note as a full-width banner on the landing/splash pages of a
        // version (which PageTitle skips), and suppresses the plugin's banner.
        Banner: './src/components/Banner.astro',
      },
      // The sidebar of the root: docs/version-config.json of the tree the root
      // is built from, like every version's (see ../versions.mjs).
      sidebar: currentSidebar(),
    }),
    favicons({
      input: {
        favicons: ['../docs/assets/logo.svg']
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
