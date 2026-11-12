// The Slack message announcing a release, given to semantic-release-slack-bot as its
// onSuccessFunction (.releaserc.mjs). The plugin's own message puts the whole notes in one
// section block, truncated at 2900 characters, and shows the <details> and <summary> tags of the
// GitHub body as raw text. This one turns the summaries into titles and gives each section of
// the notes its own blocks, so the notes go out whole; the GitHub body is left as it is.

import { createRequire } from "node:module";
import path from "node:path";

// Slack's limits: the text of a section block, and the blocks of a message.
export const MAX_TEXT = 3000;
export const MAX_BLOCKS = 50;

// The converter and the chunkifier the plugin uses, from the plugin itself. The plugin is installed
// next to semantic-release by cycjimmy/semantic-release-action (or npx for `task
// release-dry-run`), not in this repository, so it is resolved from the script that runs
// semantic-release, as releaseNotesGenerator() in .releaserc.mjs resolves its generator. The
// plugin calls onSuccessFunction without awaiting it, so this is a synchronous require; both
// modules are CommonJS.
export function slackBot(from = process.argv[1] || path.join(process.cwd(), "index.js")) {
  const main = createRequire(from).resolve("semantic-release-slack-bot");
  const require = createRequire(main);
  return {
    slackify: require("slackify-markdown"),
    chunkifyArray: require("./lib/chunkifier").chunkifyArray,
    getRepoInfo: require("./lib/getRepoInfo"),
  };
}

// The plugin's onSuccessFunction: it returns the message the plugin posts. Synchronous, since
// the plugin posts what it returns without awaiting it.
export function onSuccessFunction(pluginConfig, context, from) {
  const { slackify, chunkifyArray, getRepoInfo } = slackBot(from);
  const { nextRelease, options, env = {} } = context;
  const repo = getRepoInfo(options.repositoryUrl);
  return slackMessage(
    nextRelease.notes ?? "",
    {
      // The plugin's own order for the package name.
      packageName: env.SEMANTIC_RELEASE_PACKAGE || pluginConfig.packageName || env.npm_package_name,
      version: nextRelease.version,
      gitTag: nextRelease.gitTag,
      repoPath: repo.path,
      repoUrl: repo.URL,
    },
    { slackify, chunkifyArray },
  );
}

// The message for the notes of a release: the plugin's header with a link to the release, then
// the blocks of each section of the notes, at most MAX_BLOCKS in all.
export function slackMessage(notes, release, { slackify, chunkifyArray }) {
  const { packageName, version, gitTag, repoPath, repoUrl } = release;
  const releaseUrl = `${repoUrl}/releases/tag/${gitTag}`;
  const header = section(
    `A new version of \`${packageName}\` has been released!\nCurrent version is *${version}*\n` +
      `:package: *<${repoUrl}|${repoPath}>:*   <${releaseUrl}|${gitTag}>`,
  );
  let blocks = [header];
  for (const part of sections(slackMarkdown(notes))) {
    // slackify-markdown escapes the # of the pull request numbers, and Slack shows the backslash.
    const text = slackify(part).replaceAll("\\#", "#").trim();
    if (text !== "") {
      blocks.push(...chunks(text, chunkifyArray).map(section));
    }
  }
  if (blocks.length > MAX_BLOCKS) {
    blocks = [...blocks.slice(0, MAX_BLOCKS - 1), section(`<${releaseUrl}|Full notes on GitHub>`)];
  }
  return {
    blocks,
    // The notification's text, and the message of the clients that show no blocks.
    text: `A new version of ${packageName} has been released! Current version is ${version}: ${releaseUrl}`,
  };
}

function section(text) {
  return { type: "section", text: { type: "mrkdwn", text } };
}

// The notes without the HTML Slack cannot show: a <details> block opens and a <summary> is a title
// in bold on its own line.
export function slackMarkdown(notes) {
  return notes
    .replace(/<\/?details>/g, "\n")
    .replace(/<summary>(.*?)<\/summary>/gs, (_, title) => `\n\n**${title.trim()}**\n\n`)
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

// The notes split before each ## and ### heading, outside code blocks: the text before the first
// heading is a section of its own.
export function sections(markdown) {
  const result = [];
  let current = [];
  let fenced = false;
  for (const line of markdown.split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) {
      fenced = !fenced;
    }
    if (!fenced && /^#{2,3}\s/.test(line) && current.length > 0) {
      result.push(current.join("\n"));
      current = [];
    }
    current.push(line);
  }
  result.push(current.join("\n"));
  return result.filter((s) => s.trim() !== "");
}

// A line all in bold: a heading, a summary or a group title of the notes.
const title = /^​?\*[^*\n]+\*​?$/;

// The Slack text of a section in pieces of at most MAX_TEXT characters, cut between its items: a
// line indented under the previous one (a bullet's continuation, a nested list) stays with it, and
// so does the line after a title, so a title never ends a block. The plugin's chunkifyArray packs
// the items; an item longer than a block on its own is cut short.
export function chunks(text, chunkifyArray) {
  const items = [];
  let glued = false;
  for (const line of text.split("\n")) {
    if (items.length > 0 && (glued || /^\s+\S/.test(line))) {
      items[items.length - 1] += `\n${line}`;
    } else {
      items.push(line);
    }
    if (line.trim() !== "") {
      glued = title.test(line);
    }
  }
  const fitted = items.map((item) => (item.length > MAX_TEXT ? `${item.slice(0, MAX_TEXT - 1)}…` : item));
  return chunkifyArray(fitted, MAX_TEXT, "\n").filter((chunk) => chunk.trim() !== "");
}
