// The Slack message of a release (release-slack.mjs), with the converter and the chunkifier of the
// semantic-release-slack-bot the Release workflow installs: `task test-release` installs it in a
// temporary directory and gives its path in RELEASE_SLACK_BOT_FROM.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, it } from "node:test";

import { MAX_BLOCKS, MAX_TEXT, onSuccessFunction, slackBot, slackMessage } from "./release-slack.mjs";

const from = process.env.RELEASE_SLACK_BOT_FROM;
if (!from) {
  throw new Error("RELEASE_SLACK_BOT_FROM is not set: run `task test-release`");
}
const bot = slackBot(from);

const release = {
  packageName: "kube-image-keeper",
  version: "3.0.0-beta.5",
  gitTag: "v3.0.0-beta.5",
  repoPath: "enix/kube-image-keeper",
  repoUrl: "https://github.com/enix/kube-image-keeper",
};
const releaseUrl = "https://github.com/enix/kube-image-keeper/releases/tag/v3.0.0-beta.5";

const beta5 = readFileSync(path.join(import.meta.dirname, "testdata", "release-notes-3.0.0-beta.5.md.txt"), "utf8");

const texts = (message) => message.blocks.map((block) => block.text.text);
// slackify-markdown wraps bold text in zero-width spaces.
const lines = (text) => text.replaceAll("​", "").split("\n");
const bullets = (message) => texts(message).flatMap(lines).filter((line) => line.startsWith("•"));

function checkLimits(message) {
  assert.ok(message.blocks.length <= MAX_BLOCKS, `${message.blocks.length} blocks`);
  for (const text of texts(message)) {
    assert.ok(text.length <= MAX_TEXT, `a block of ${text.length} characters`);
    assert.ok(text.trim() !== "", "an empty block");
    // A block never starts inside a bullet: on its continuation or a nested line.
    assert.doesNotMatch(text, /^\s/);
  }
}

describe("the Slack message of a release", () => {
  describe("with the notes of 3.0.0-beta.5", () => {
    const message = slackMessage(beta5, release, bot);
    console.log(JSON.stringify(message, null, 2));

    it("starts with the header and the link to the release", () => {
      assert.equal(
        message.blocks[0].text.text,
        "A new version of `kube-image-keeper` has been released!\nCurrent version is *3.0.0-beta.5*\n" +
          `:package: *<https://github.com/enix/kube-image-keeper|enix/kube-image-keeper>:*   <${releaseUrl}|v3.0.0-beta.5>`,
      );
    });

    it("keeps a plain text fallback with the version and the link", () => {
      assert.equal(
        message.text,
        `A new version of kube-image-keeper has been released! Current version is 3.0.0-beta.5: ${releaseUrl}`,
      );
    });

    it("leaves no details or summary tag", () => {
      for (const text of texts(message)) {
        assert.doesNotMatch(text, /<\/?(details|summary)>|&lt;\/?(details|summary)&gt;/);
      }
    });

    it("shows the pull request numbers without a backslash", () => {
      const all = texts(message).join("\n");
      assert.match(all, /\(#\d+/);
      assert.doesNotMatch(all, /\\#/);
    });

    it("never ends a block on a title", () => {
      for (const text of texts(message)) {
        assert.doesNotMatch(lines(text).at(-1), /^\*[^*]+\*$/);
      }
    });

    it("turns each summary into a title line in bold", () => {
      const summaries = [...beta5.matchAll(/<summary>(.*?)<\/summary>/g)].map((m) => m[1]);
      assert.ok(summaries.length > 0, "the notes have no summary");
      const all = texts(message).flatMap(lines);
      for (const summary of summaries) {
        assert.ok(all.includes(`*${summary}*`), `no title *${summary}*`);
      }
    });

    it("starts a block at each section of the notes", () => {
      const headings = [...beta5.matchAll(/^#{2,3} (.*)$/gm)].map((m) => m[1]);
      assert.ok(headings.length >= 4, `${headings.length} headings`);
      const firstLines = texts(message).map((text) => lines(text)[0]);
      for (const heading of headings) {
        assert.ok(firstLines.includes(`*${heading}*`), `no block starts with *${heading}*`);
      }
    });

    it("keeps every bullet of the notes, whole", () => {
      assert.equal(bullets(message).length, beta5.match(/^- /gm).length);
    });

    it("stays within the limits of Slack", () => checkLimits(message));
  });

  describe("with a section longer than a block", () => {
    const item = (i) => `- Item ${i} ${"x".repeat(100)}\n  continued on a second line.`;
    const notes = `## Fixes\n\n${Array.from({ length: 100 }, (_, i) => item(i)).join("\n")}\n`;
    const message = slackMessage(notes, release, bot);

    it("splits it between its bullets, over several blocks", () => {
      assert.ok(message.blocks.length > 3, `${message.blocks.length} blocks`);
      checkLimits(message);
      assert.deepEqual(
        bullets(message).map((line) => line.match(/Item \d+/)[0]),
        Array.from({ length: 100 }, (_, i) => `Item ${i}`),
      );
    });

    it("keeps the second line of each bullet with it", () => {
      for (const text of texts(message).slice(1)) {
        const all = text.split("\n").filter((line) => line.trim() !== "");
        assert.equal(all.filter((line) => line.includes("continued")).length, all.filter((line) => line.startsWith("•")).length);
      }
    });
  });

  describe("with more blocks than Slack takes", () => {
    const notes = Array.from({ length: 60 }, (_, i) => `## Section ${i}\n\n- An item.`).join("\n\n");
    const message = slackMessage(notes, release, bot);

    it("stops before the limit and links the full notes on GitHub", () => {
      assert.equal(message.blocks.length, MAX_BLOCKS);
      assert.equal(message.blocks.at(-2).text.text.replaceAll("​", ""), `*Section ${MAX_BLOCKS - 3}*\n\n•   An item.`);
      assert.equal(message.blocks.at(-1).text.text, `<${releaseUrl}|Full notes on GitHub>`);
      checkLimits(message);
    });
  });

  describe("with no notes", () => {
    it("is the header alone", () => {
      assert.equal(slackMessage("", release, bot).blocks.length, 1);
    });
  });

  describe("as the onSuccessFunction of the plugin", () => {
    it("builds the message from the release semantic-release gives", () => {
      const message = onSuccessFunction(
        { packageName: "kube-image-keeper" },
        {
          nextRelease: { notes: beta5, version: "3.0.0-beta.5", gitTag: "v3.0.0-beta.5" },
          options: { repositoryUrl: "https://github.com/enix/kube-image-keeper.git" },
          env: {},
        },
        from,
      );
      assert.deepEqual(message, slackMessage(beta5, release, bot));
    });
  });
});
