// semantic-release configuration. JavaScript rather than JSON because `notesPattern`
// below is a function, which JSON cannot carry.

// conventional-commits-parser builds its note regex case-insensitively by default, and
// matches it at the start of every line of the body. Any wrapped line starting with the
// words "breaking change" is then read as a breaking-change footer. Keep the default
// pattern, drop the "i" flag, so only the upper-case footer the convention defines triggers a major.
const parserOpts = {
  notesPattern: (keywords) => new RegExp(`^[\\s|*]*(${keywords})[:\\s]+(.*)`),
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
    [
      "@semantic-release/release-notes-generator",
      {
        preset: "conventionalcommits",
        parserOpts,
      },
    ],
    [
      "@semantic-release/changelog",
      {
        changelogFile: "CHANGELOG.md",
      },
    ],
    [
      "@semantic-release/github",
      {
        assets: ["CHANGELOG.md", "../assets/*"],
        successComment: false,
        failComment: false,
      },
    ],
  ],
  branches: [
    {
      name: "main",
      prerelease: "alpha",
    },
    {
      name: "release",
    },
  ],
};
