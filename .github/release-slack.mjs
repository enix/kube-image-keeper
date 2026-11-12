// The Slack message announcing a release, given to semantic-release-slack-bot as its
// onSuccessFunction (.releaserc.mjs). The plugin's own message puts the whole notes in one
// section block, truncated at 2900 characters, and shows the <details> and <summary> tags of the
// GitHub body as raw text. This one drops them, summaries included, and gives each section of
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
    const text = linkRefs(slackify(part).replaceAll("\\#", "#"), repoUrl).trim();
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

// The notes without the HTML Slack cannot show: a <details> block opens and its <summary> is
// dropped, since Slack cannot collapse the list and the summary only counts its items.
export function slackMarkdown(notes) {
  return notes
    .replace(/<\/?details>/g, "\n")
    .replace(/<summary>(.*?)<\/summary>/gs, "\n\n")
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

// A line all in bold: a heading or a group title of the notes.
const title = /^​?\*[^*\n]+\*​?$/;

// What a reference never touches: inline code and the links of the Slack text.
const protectedSpan = /(`[^`\n]*`|<[^<>\n]*>)/;
// A commit sha, letters and digits both, so a number or a word is never taken for one.
const sha = /(?<![\w-])(?=[0-9a-f]*[a-f])(?=[0-9a-f]*\d)[0-9a-f]{7,40}(?![\w-])/g;
// #N, or owner/repo#N.
const ref = /(?<![\w&/#.-])(?:([\w.-]+\/[\w.-]+))?#(\d+)(?!\w)/g;

// The Slack text with its references as links, as GitHub autolinks them in the release body: #N
// and owner/repo#N to the issue (GitHub redirects /issues/N to the pull request when N is one),
// and a commit sha inside parentheses to the commit. Code, existing links and titles are left
// as they are.
export function linkRefs(text, repoUrl) {
  const { origin, pathname } = new URL(repoUrl);
  const repoPath = pathname.replace(/^\/|\/$/g, "");
  const plain = (s, fn) =>
    s
      .split(protectedSpan)
      .map((part, i) => (i % 2 === 1 ? part : fn(part)))
      .join("");
  let fenced = false;
  return text
    .split("\n")
    .map((line) => {
      if (line.trimStart().startsWith("```")) {
        fenced = !fenced;
        return line;
      }
      if (fenced || title.test(line)) {
        return line;
      }
      const withShas = plain(line, (s) =>
        s.replace(/\([^()]*\)/g, (group) => group.replace(sha, (c) => `<${origin}/${repoPath}/commit/${c}|${c}>`)),
      );
      return plain(withShas, (s) =>
        s.replace(ref, (match, repo, n) => `<${origin}/${repo ?? repoPath}/issues/${n}|${match}>`),
      );
    })
    .join("\n");
}

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
