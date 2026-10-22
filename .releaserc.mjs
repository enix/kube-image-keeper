// semantic-release configuration. JavaScript rather than JSON because `notesPattern`
// below is a function, which JSON cannot carry.

import { readFileSync } from "node:fs";

// conventional-commits-parser builds its note regex case-insensitively by default, and
// matches it at the start of every line of the body. Any wrapped line starting with the
// words "breaking change" is then read as a breaking-change footer. Keep the default
// pattern, drop the "i" flag, so only the upper-case footer the convention defines triggers a major.
const parserOpts = {
  notesPattern: (keywords) => new RegExp(`^[\\s|*]*(${keywords})[:\\s]+(.*)`),
};

// From the beta on, the Release workflow sets RELEASE_NOTES_FILE to the notes written for
// the release, rendered from the release-notes.yaml asset of its draft release
// (CONTRIBUTING.md, "Release notes"). They replace
// the generated notes, so the GitHub release, the Slack message and the workflow outputs all
// carry them. The file is read in the generateNotes step, which runs before the tag is
// pushed: an error there fails the release, while an error at load would make
// semantic-release silently fall back on its default configuration.
const notesFile = process.env.RELEASE_NOTES_FILE;

export default {
  plugins: [
    [
      "@semantic-release/commit-analyzer",
      {
        preset: "conventionalcommits",
        parserOpts,
      },
    ],
    !notesFile
      ? [
          "@semantic-release/release-notes-generator",
          {
            preset: "conventionalcommits",
            parserOpts,
          },
        ]
      : // An inline plugin: semantic-release takes an object of step functions as a plugin.
        { generateNotes: () => readFileSync(notesFile, "utf8") },
    [
      "@semantic-release/github",
      {
        // Comment on the issues and pull requests a release fixes, but only when it
        // reaches users: an alpha or an rc resolves nothing for them. Testing
        // branch.prerelease rather than the channel keeps the comment on a maintenance
        // line, whose stable releases carry a channel of their own.
        successCommentCondition: "<% return !branch.prerelease %>",
        // Never open an issue when a release fails: it is dispatched by hand, the
        // workflow run is where to look, and nobody closes those issues.
        failCommentCondition: false,
      },
    ],
    [
      "semantic-release-slack-bot",
      {
        // The name the Slack message gives the project. The plugin falls back on
        // npm_package_name, which is empty here: no package.json at the root.
        packageName: "kube-image-keeper",
        notifyOnSuccess: true,
        notifyOnFail: false,
        markdownReleaseNotes: true,
      },
    ],
  ],
  branches: [
    {
      name: "main",
      prerelease: "beta",
    },
    {
      name: "release",
    },
  ],
};
