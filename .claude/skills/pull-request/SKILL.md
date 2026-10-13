---
name: pull-request
description: Use when opening a pull request on kuik, rewriting its description, or answering its automated review; prepares the branch, drafts a short readable description for the user to confirm, and keeps it in step with the branch.
argument-hint: "[PR number]"
allowed-tools: Bash(git log *) Bash(git diff *) Bash(git status *) Bash(git fetch origin) Bash(gh pr view *) Bash(gh pr diff *) Bash(gh pr checks *) Bash(hack/wait-coderabbit.sh *) Bash(hack/coderabbit-reviewed.sh *) Read Grep Glob Write
---

# Pull request

Target: `$ARGUMENTS` (an existing PR number, or empty to open one from the current branch).
Every step that writes on GitHub (create, edit, comment, resolve) waits for the user's
explicit go: show the text first, then run the command.

A pull request stays issue-sized, about 30 specs at most, whether it answers an issue, a
request or one piece of an epic cut into several, and is followed with a task list: see
the [`test-outline`](../test-outline/SKILL.md) skill.

## 1. Prepare the branch

- Branch off a fresh `origin/main` (`git fetch origin` first); never merge `main` into the
  branch. Rebase on `main` only when needed (step 6): `gh pr merge --rebase` replays the
  commits on `main` anyway.
- Per component, a `test(<scope>): ...` commit with its specs, then the `feat(<scope>): ...`
  commit that makes them green ([`tests.md`](../../rules/tests.md)). The `feat` commit
  carries the docs and the line in any index (`AGENTS.md`, a README), and touches no
  `*_test.go` file or test helper package. A component without specs (a skill, a task) is
  one complete commit.
- Check `git log --stat origin/main..HEAD`: every test file comes from a `test` commit and
  no `feat` commit touches one. The reviewer checks the same thing.
- No commit that only indexes or fixes the previous ones: before the PR opens, fold a fix
  into its commit with `git commit --fixup=<sha>`, then autosquash in place as in step 5.
  Once it is open, fixes stay `fixup!` commits until the review is over (step 5), and so does
  the regression spec of a bug in the branch's own code: a `fixup!` of the `test` commit of
  its component. The exception is a bug in code no commit of the branch owns: the fix is
  its own commit, and its regression spec its own `test` commit after it, subject
  `test(<scope>): add regression specs for <what>`, body naming the review, the component
  or commit subject of the fix (never a sha: hashes change on rebase) and that the specs pin
  the fix ([`tests.md`](../../rules/tests.md)). The description names it in one line. A
  late batch of review fixes is the other exception (step 5). Review fixes need no
  approval; a fix in a shared package (`internal/registry/*`, `internal/status/*`,
  `internal/auth/*`, `internal/routing/*`, `internal/imagepath`, `internal/config`) lands
  first in its own pull request.
- Before the pull request opens, a fresh `reviewer` agent reviews the whole branch
  (`origin/main...HEAD`): spec conformance and the interactions between components. The
  `spec-reviewer` agent reviews the outline only; a `reviewer` reviews each `feat` commit as
  it lands; CodeRabbit reviews the pull request last. The Claude code-review plugin is not
  used ([0014](../../../notes/0014-implementer-and-reviewer-agents.md)).
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
- When the PR decided a spec reading (where `docs/v3/` is silent or ambiguous) or a
  behaviour choice, add `Decisions:` after the list, one line each with the link to the spec
  heading: the reviewer weighs in there.
- Short sentences, one idea each. A block of dense prose is a failure: split it or cut it.
- Every "What to look at" bullet that rests on a spec section or a note links it by its
  absolute URL (`https://github.com/enix/kube-image-keeper/blob/main/<path>#<heading-slug>`):
  GitHub resolves a relative link against the PR URL and it 404s. Link a file the PR adds
  on `main` too, marked "added by this PR": the link works once merged, and until then the
  file is in the diff. Link an issue or a PR by its URL. Link instead of paraphrasing.
- The verification line may say, in one line, that the tests came first and where to see
  it (`git log --stat`: no `feat` commit touches a test file).
- Never paste the test outline: the `Test outline comment` workflow posts the cases the PR
  adds and removes as a comment, and updates it on every push.
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

- As soon as a review is due (the PR opened, `@coderabbitai review` posted), run
  `hack/wait-coderabbit.sh <n>` in the background and go on. When it exits 0, the review
  and its check are done: triage it at once (below). When it exits 3, CodeRabbit is rate
  limited: follow the rate limit bullet below. When it exits 1, no review came within the
  hour: tell the user.
- CodeRabbit reviews once when the PR opens. For later commits, comment
  `@coderabbitai review`. After a force-push, comment `@coderabbitai full review` instead
  only when the PR's own patch changed (the patch check of the `e2e-ready` bullet below
  differs: fixups pushed while it was rate limited, a conflict resolved in the PR's lines):
  the incremental review lost its base. A rebase that only brings `main`'s changes, or an
  autosquash of reviewed fixups, needs none, and CodeRabbit answers it "No files to
  review".
- CodeRabbit answers only comments that mention `@coderabbitai` (`chat.auto_reply: false`
  in `.coderabbit.yaml`), a top-level comment or a reply in one of its threads alike.
- When CodeRabbit answers that it is rate limited or out of review quota, never ask again
  in a loop: the quota is shared by every session. Report it to the session that
  coordinates the others (the user when there is none), which checks the remaining
  capacity once for all and says when to ask again; ask once more then. If it still cannot
  review, the PR waits: only the user's explicit waiver, possibly relayed by the
  coordinating session, lets it go on without CodeRabbit.
- Never rewrite the branch under review (no rebase, amend or force-push): the incremental
  review would lose its base. A fix is a `git commit --fixup=<sha>` pushed as is. The
  `conform` check stays red on `fixup!` commits, which keeps the PR from merging unsquashed.
- Do not push while a review runs: CodeRabbit drops it ("head changed") and must be asked
  again.
- Read the body of each review, not only its threads: the `quiet` profile folds findings
  into the review body without opening a thread.
- Triage each comment: fix it (a `fixup!` commit), or accept it as a limit and say so in
  the description. An exception is a late batch of fixes touching code that several later
  commits rewrite: regression pairs at the end of the branch instead
  ([`tests.md`](../../rules/tests.md)).
- Resolve a thread once its fix is pushed. Reply only when resolving without a fix: one
  sentence on why. CONTRIBUTING asks the author to answer reviews: post the reply yourself,
  the outbound hook asks the user before every write on GitHub. No thread is left open at
  the merge: this lists the open ones, and must print nothing.

  ```sh
  gh api graphql -f query='query { repository(owner: "enix", name: "kube-image-keeper") { pullRequest(number: <n>) { reviewThreads(first: 100) { nodes { id isResolved path } } } } }' --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved | not)'
  ```

- When the review is over, autosquash in place and force-push once (`--force-with-lease`).
  In place means on the commit of `main` the branch started from, not on the current
  `main`: the branch does not move, and `git diff <head before> HEAD` stays empty. Print
  that commit with `git merge-base origin/main HEAD`, then squash on its sha:

  ```sh
  GIT_SEQUENCE_EDITOR=: git rebase -i --autosquash <sha>
  ```

  Never `origin/main` or `main` in that command: another session may have fetched a newer
  `main`, and the branch would silently move onto it. Before the force-push, check that
  `git diff <head before> HEAD` prints nothing; if it prints anything, the base moved or the
  content changed: go back with `git reset --keep <head before>` and redo it on the sha.

  Then run `task check-commits` in the background (it takes minutes): in a scratch
  worktree, every commit builds and vets, and every `feat` commit passes `task test`. Go on
  meanwhile, and read its result before the force-push. Catching up with `main` is not part
  of the autosquash (step 6).
- Add the `e2e-ready` label (`gh pr edit <n> --add-label e2e-ready`, no question asked)
  only once the review is over, the final e2e `test` commit pushed and the branch
  autosquashed, and enable auto-merge right after it (step 6). The review is over when
  both hold, checked, never assumed:
  - CodeRabbit has reviewed the PR's own changes as they stand:
    `hack/coderabbit-reviewed.sh <n>`, run from the PR's worktree, exits 0. It compares the
    diff against `main` of CodeRabbit's last reviewed commit with `HEAD`'s (`git patch-id
    --verbatim`: line offsets do not count, whitespace does), so an autosquash of reviewed
    fixups, or a rebase that only brings `main`'s changes, needs no new review. Exit 1: ask
    for a review. Exit 2: it cannot tell, report it. A "Review rate limited", "Review in
    progress" or "Review skipped" status is not a review. Only the user's explicit waiver,
    possibly relayed by the coordinating session, lifts this point.

  - The open threads query above prints nothing.

  Add the label only once the `E2E` run of the last push has finished (it fails within
  seconds without the label: `gh pr checks <n>` shows it). Both runs share one concurrency
  group: a label added while the push's run starts can get its own run cancelled.

  The required `E2E` check fails until the label is there. Skip it when the PR changes no
  path the suite depends on (the list in `.github/workflows/e2e.yaml`): `E2E` passes
  without it. The suite then runs once. If it fails, push `fixup!` commits: `E2E` fails at
  once without running the suite until the next autosquash and force-push, which runs it
  again.

## 6. Merge

Rebase on `main` only when GitHub cannot merge (`mergeStateStatus` is `DIRTY`) or the
branch needs something `main` brought: `git fetch origin` first, rebase on `origin/main`,
check the rebase with `git range-diff ORIG_HEAD...HEAD`, push it with
`--force-with-lease`, then wait for the checks again. Ask `@coderabbitai full review` only
when the PR's own patch changed (the patch check of step 5): a rebase that only brings
`main`'s changes needs none.

Enable auto-merge at the moment the `e2e-ready` label goes on (step 5), under the same
conditions: the review is over, checked as step 5 says. A PR that needs no label gets it
once those conditions hold. The checks need not be green yet. Run it alone in its command:
the outbound hook asks the user first, and their confirmation is the go to merge (they may
want to test the PR before giving it). Pin the head the user saw, so a later push cannot be
merged on the same go. When CodeRabbit's review was waived, say so in the message that
precedes the command: the prompt itself does not.

```sh
gh pr merge <n> --auto --rebase --match-head-commit <HEAD sha>
```

GitHub merges once `Conform`, `Test` and `E2E` pass and no review thread is open. Follow it
in the background until `gh pr view <n> --json state,mergedAt` shows the merge. Wait for
checks with `gh pr checks <n> --watch` run in the background, here and in every step, never
with a sleep loop:

- Before any push to a PR whose auto-merge is enabled (a fix for a failed check, an
  autosquash, a rebase), disable it first, alone in its command:
  `gh pr merge <n> --disable-auto`. Otherwise GitHub merges as soon as the checks pass,
  before CodeRabbit sees the change. Then push, wait until CodeRabbit has reviewed the PR's
  own changes as they stand (the patch check of step 5), and enable auto-merge again: the
  outbound hook asks the user, as the first time.
- Auto-merge stalls: every required check is green and `mergeStateStatus` is `CLEAN`, yet
  10 minutes later the PR is still open. GitHub re-evaluates auto-merge only on an event of
  the pull request, and a check run cancelled by a concurrency group can make it miss the
  last one. The user's go covers this merge, for the head they saw only: run
  `gh pr merge <n> --rebase --match-head-commit <the sha auto-merge was enabled on>`, and
  tell the user auto-merge stalled.

Confirm the merge with `gh pr view <n> --json state,mergedAt`. If that call is refused, ask
the user to type `! gh pr view <n> --json state,mergedAt` in the Claude Code prompt (the
`!` prefix runs it in the session): never report a merge you could not confirm.
