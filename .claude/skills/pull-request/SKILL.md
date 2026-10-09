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

## 0. Size the pull request

An epic (a feature too large for one review) never ships as one pull request. It is cut
into issue-sized pull requests of about 30 specs, each mergeable on its own: `main` stays
coherent, nothing is half-wired, and each carries its docs and, last, its e2e specs.
Propose the cut once the outline exists; the maintainer validates it before any body is
written. The issues are grouped under the GitHub milestones of the release: its beta, then
its GA.

## 1. Prepare the branch

- Rebase on `origin/main` (`git fetch origin` first); never merge `main` into the branch.
- Per component, a `test(<scope>): ...` commit with its specs, then the `feat(<scope>): ...`
  commit that makes them green ([`tests.md`](../../rules/tests.md)). The `feat` commit
  carries the docs and the line in any index (`AGENTS.md`, a README), and touches no
  `*_test.go` file or test helper package. A component without specs (a skill, a task) is
  one complete commit.
- Check `git log --stat origin/main..HEAD`: every test file comes from a `test` commit and
  no `feat` commit touches one. The reviewer checks the same thing.
- No commit that only indexes or fixes the previous ones: before the PR opens, fold a fix
  into its commit with `git commit --fixup=<sha>` and `git rebase -i --autosquash origin/main`.
  Once it is open, fixes stay `fixup!` commits until the review is over (step 5). The exception is
  a regression spec for a bug a review found: its own `test` commit after the fix, subject
  `test(<scope>): add regression specs for <what>`, body naming the review, the component
  or commit subject of the fix (never a sha: hashes change on rebase) and that the specs pin
  the fix ([`tests.md`](../../rules/tests.md)). The description
  names it in one line.
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
- Every "What to look at" bullet that rests on a spec section or a note links it by its
  absolute URL (`https://github.com/enix/kube-image-keeper/blob/main/<path>#<heading-slug>`):
  GitHub resolves a relative link against the PR URL and it 404s. Link a file the PR adds
  on `main` too, marked "added by this PR": the link works once merged, and until then the
  file is in the diff. Link an issue or a PR by its URL. Link instead of paraphrasing.
- The verification line may say, in one line, that the tests came first and where to see
  it (`git log --stat`: no `feat` commit touches a test file).
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
gh pr create --title '<subject>' --body-file <file>   # new PR, ready for review
gh pr edit <n> --body-file <file>                      # existing PR
```

Write the body to a file first: quoting a long body inline breaks.

## 4. Keep it in step

Every time the branch changes (a commit added, dropped, split or amended), re-read the
description against `git log origin/main..HEAD` and the diff. When a sentence no longer
holds, draft the fix, show it, and edit after the user confirms. A description that
describes a dropped file misleads the reviewer and CodeRabbit alike.

## 5. The automated review

- CodeRabbit reviews once when the PR opens. For later commits, comment
  `@coderabbitai review`. After a force-push (a rebase, the autosquash), comment
  `@coderabbitai full review` instead: the incremental review lost its base.
- CodeRabbit answers only comments that mention `@coderabbitai` (`chat.auto_reply: false`
  in `.coderabbit.yaml`), a top-level comment or a reply in one of its threads alike.
- Never rewrite the branch under review (no rebase, amend or force-push): the incremental
  review would lose its base. A fix is a `git commit --fixup=<sha>` pushed as is. The
  `conform` check stays red on `fixup!` commits, which keeps the PR from merging unsquashed.
- Do not push while a review runs: CodeRabbit drops it ("head changed") and must be asked
  again.
- Triage each comment: fix it (a `fixup!` commit), or accept it as a limit and say so in
  the description.
- Resolve a thread once its fix is pushed. Reply only when resolving without a fix: one
  sentence on why. CONTRIBUTING asks the author to answer reviews: post the reply yourself,
  the outbound hook asks the user before every write on GitHub.
- When the review is over, autosquash and force-push once. `git diff <head before> HEAD`
  must be empty: the content did not change, so no new review is needed.
- Once the review is over, the final e2e `test` commit pushed and the branch autosquashed,
  ask the user to add the `e2e-ready` label (`gh pr edit <n> --add-label e2e-ready`): the
  required `E2E` check fails until it is there. Skip this when the PR changes no path the
  suite depends on (the list in `.github/workflows/e2e.yaml`): `E2E` passes without it.
  It is a GitHub write: the user adds it or approves the command. The suite then runs once. If it fails, push `fixup!` commits: `E2E`
  fails at once without running the suite until the next autosquash and force-push, which
  runs it again.
