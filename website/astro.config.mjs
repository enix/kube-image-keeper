// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import favicons from 'astro-favicons';
import remarkGithubAdmonitionsToDirectives from 'remark-github-admonitions-to-directives';
import rehypeAstroRelativeMarkdownLinks from 'astro-rehype-relative-markdown-links';
import { syncDocs } from './scripts/sync-docs.mjs';

// Sync ../docs + overlay into src/content/docs before content collections load.
// Runs for every astro command (build, dev, CI via withastro/action), so the
// gitignored collection dir is always present and current.
const syncDocsIntegration = {
  name: 'sync-docs',
  hooks: {
    'astro:config:setup': () => syncDocs(),
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
      components: {
        SiteTitle: './src/components/SiteTitle.astro',
        Head: './src/components/Head.astro',
      },
      sidebar: [
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
