// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import favicons from 'astro-favicons';
import remarkGithubAdmonitionsToDirectives from 'remark-github-admonitions-to-directives';

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
  },
  integrations: [
    starlight({
      title: 'kube-image-keeper',
      description: 'Documentation for kube-image-keeper (kuik), the Kubernetes operator for container image routing, mirroring, and replication.',
      markdown: {
        // Starlight only applies its remark/rehype passes (asides, heading links) to its own collection dir by default, so register ../docs explicitly.
        processedDirs: ['../docs'],
      },
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
