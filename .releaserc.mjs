// semantic-release configuration. JavaScript rather than JSON because `notesPattern`
// below is a function, which JSON cannot carry.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

// conventional-commits-parser builds its note regex case-insensitively by default, and
// matches it at the start of every line of the body. Any wrapped line starting with the
// words "breaking change" is then read as a breaking-change footer. Keep the default
// pattern, drop the "i" flag, so only the upper-case footer the convention defines triggers a major.
const parserOpts = {
  notesPattern: (keywords) => new RegExp(`^[\\s|*]*(${keywords})[:\\s]+(.*)`),
};

// The Release workflow sets RELEASE_NOTES_FILE to the notes written for
// the release, rendered from the release-notes.yaml asset of its draft release
// (CONTRIBUTING.md, "Release notes"). They replace
// the generated notes, so the GitHub release, the Slack message and the workflow outputs all
// carry them. The file is read in the generateNotes step, which runs before the tag is
// pushed: an error there fails the release, while an error at load would make
// semantic-release silently fall back on its default configuration.
const notesFile = process.env.RELEASE_NOTES_FILE;

// RELEASE_NOTES_SOURCE is the release-notes.yaml those notes are rendered from, the asset of the
// draft release the workflow downloaded: the published release keeps it as its own asset, the
// source a later release's notes start from.
const notesSource = process.env.RELEASE_NOTES_SOURCE;

// The changelog semantic-release generates from the commits, which every release attaches as
// its CHANGELOG.md asset next to the written notes of its body. It is written outside the
// checkout, so the release leaves the working tree as it found it.
const changelogFile = path.join(tmpdir(), `kuik-release-${process.pid}`, "CHANGELOG.md");

// The generator is a dependency of semantic-release, installed next to it by
// cycjimmy/semantic-release-action (or npx for `task release-dry-run`), not in this
// repository: a static import from this file would not find it. It is resolved from the script
// that runs semantic-release, and imported when the notes are generated, never at load.
async function releaseNotesGenerator() {
  const name = "@semantic-release/release-notes-generator";
  const require = createRequire(process.argv[1] || path.join(process.cwd(), "index.js"));
  for (const dir of require.resolve.paths(name) ?? []) {
    const root = path.join(dir, name);
    if (existsSync(path.join(root, "package.json"))) {
      const pkg = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
      return import(pathToFileURL(path.join(root, pkg.main || "index.js")).href);
    }
  }
  throw new Error(`${name} not found next to ${process.argv[1]}`);
}

// An inline plugin: semantic-release takes an object of step functions as a plugin. It runs the
// release notes generator with the options it would get as a plugin, writes its changelog to
// changelogFile, and returns the written notes when the workflow gives them.
const notes = {
  async generateNotes(pluginConfig, context) {
    const { generateNotes } = await releaseNotesGenerator();
    const changelog = await generateNotes({ ...pluginConfig, preset: "conventionalcommits", parserOpts }, context);
    mkdirSync(path.dirname(changelogFile), { recursive: true });
    writeFileSync(changelogFile, changelog);
    return notesFile ? readFileSync(notesFile, "utf8") : changelog;
  },
};

export default {
  plugins: [
    [
      "@semantic-release/commit-analyzer",
      {
        preset: "conventionalcommits",
        parserOpts,
      },
    ],
    notes,
    [
      "@semantic-release/github",
      {
        // The generated changelog, written by the notes plugin above for every release, and
        // the source of the written notes when the workflow gives one.
        assets: [
          { path: changelogFile, name: "CHANGELOG.md", label: "CHANGELOG.md" },
          ...(notesSource ? [{ path: notesSource, name: "release-notes.yaml", label: "release-notes.yaml" }] : []),
        ],
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
