# 0017 — A session drives its own pull request to the merge

**Date:** 2026-10-28 · **Status:** decided · Supersedes [0003](./0003-agent-orchestration.md)

**Decision:** the session that owns a pull request takes it from opening to merge on its
own: it opens it ready for review, pushes its review fixes as `fixup!` commits and folds
them by one autosquash, adds the `e2e-ready` label, resolves its review threads and enables
auto-merge pinned on the full head sha. Starting the work on a pull request is the go to
merge it (Paul, 2026-10-28). The outbound hook allows these steps on the pull request of
the current branch only, with no prompt; a push stays refused while auto-merge is on, and a
bare `git push`, a direct merge or any other pull request still asks.

**Why:**

- The work is defined before a pull request starts, and CodeRabbit and the required checks
  review it: a confirmation per step added no review, only waiting.
- Sessions stalled on prompts for pushes, labels and thread resolutions on their own pull
  request, while Paul approved them every time.
- Pinning the head sha keeps a later, unreviewed push from merging on the same go.

**Rejected:**

- Keeping the confirmation for pull requests that change a skill: Paul chose one rule for
  every pull request.
