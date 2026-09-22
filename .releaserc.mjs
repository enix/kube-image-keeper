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
