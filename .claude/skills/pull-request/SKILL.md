---
name: pull-request
description: Use when opening a pull request on kuik, rewriting its description, or answering its automated review; prepares the branch, drafts a short readable description for the user to confirm, and keeps it in step with the branch.
argument-hint: "[PR number]"
allowed-tools: Bash(git log *) Bash(git diff *) Bash(git status *) Bash(git fetch *) Bash(gh pr view *) Bash(gh pr diff *) Bash(gh pr checks *) Read Grep Glob Write
---

# Pull request

Target: `$ARGUMENTS` (an existing PR number, or empty to open one from the current branch).
Every step that writes on GitHub (create, edit, comment, resolve) waits for the user's
explicit go: show the text first, then run the command.

## 1. Prepare the branch

- Rebase on `origin/main` (`git fetch origin` first); never merge `main` into the branch.
- One commit per component, each complete: a skill, a type or a task comes with its tests,
  its docs and its line in any index (`AGENTS.md`, a README). No commit that only indexes or
  fixes the previous ones: fold a fix into the commit it belongs to with
  `git commit --fixup=<sha>` and `git rebase -i --autosquash origin/main`.
- Conventional commit subjects with the scopes of `.conform.yaml`; the PR title is the
  subject of the main commit, or a subject that covers them all.

## 2. Draft the description

Write for a reviewer who has 2 minutes. The shape:

```markdown
<1 to 3 sentences: what the PR does and why, in plain words.>

What to look at:

- **<Topic>**: <one line on what changed there, or the choice to weigh in on>.
- **<Topic>**: <...>.

Prepared with <tool>; <how it was verified, in one line>.
```

- The introduction says what the PR brings, not how the diff is organised.
- The list names the topics worth a reviewer's attention (a mechanism, a trade-off, a
  risky file), one line each, 3 to 7 entries. It never restates the commits or the diff.
- Add a line only when it has something to say: no empty or "None" section. A known limit
  (what the PR deliberately leaves out) goes in the list, or in one sentence after it.
- When the PR answers an issue, end the introduction with `Closes #N` (or `Part of #N`):
  GitHub closes it on merge and the reader gets the context from the link.
- When the PR changes what users see on upgrade (a chart value renamed or removed, a CRD
  field, a default, a behaviour), add `Upgrade notes:` after the list, 1 to 3 lines. It is
  what the release notes are written from.
- Short sentences, one idea each. A block of dense prose is a failure: split it or cut it.
- Link the spec (`docs/v3/`) or the note (`notes/`) instead of paraphrasing it.
- Keep the AI disclosure CONTRIBUTING requires (`Use of AI tools`) as the last line.

Avoid:

- Template check-lists (`- [x] tests pass`, `- [x] docs updated`): CI already says it.
- A commit-by-commit summary or a count of changed files: the diff shows it.
- Promotional wording (robust, comprehensive, seamless) and openers like "This PR aims to".
- References to the conversation that produced the PR ("as discussed", "per the agent"):
  the reviewer was not there.
- Pasted CI or test output: one line when a result matters.
- Markdown headings: the introduction and the list are enough.

Show the draft to the user and wait. Apply their changes, show again, until they confirm.

## 3. Open or update the PR

```sh
gh pr create --draft --title '<subject>' --body-file <file>   # new PR, as a draft
gh pr edit <n> --body-file <file>                              # existing PR
```

Write the body to a file first: quoting a long body inline breaks.

## 4. Keep it in step

Every time the branch changes (a commit added, dropped, split or amended), re-read the
description against `git log origin/main..HEAD` and the diff. When a sentence no longer
holds, draft the fix, show it, and edit after the user confirms. A description that
describes a dropped file misleads the reviewer and CodeRabbit alike.

## 5. The automated review

- CodeRabbit reviews once when the PR opens or is marked ready. For later commits, comment
  `@coderabbitai review` (or `@coderabbitai full review` after a rewrite).
- Do not push while a review runs: CodeRabbit drops it ("head changed") and must be asked
  again.
- Triage each comment: fix it (fold the fix into its commit, step 1), or accept it as a
  limit and say so in the description.
- Resolve a thread once its fix is pushed. Reply only when resolving without a fix: one
  sentence on why. CONTRIBUTING asks the author to answer reviews: draft the reply, the
  user posts it or tells you to.
