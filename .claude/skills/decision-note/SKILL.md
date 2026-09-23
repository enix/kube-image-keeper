---
name: decision-note
description: Write a decision note under notes/ (filter, numbered file, index line)
argument-hint: <decision in one sentence>
disable-model-invocation: true
allowed-tools: Read Grep Glob Write Edit Bash(ls *) Bash(git log *) Bash(task lint-markdown *) Bash(task lint-markdown-fix *)
---

# Write a decision note

Decision to record: $ARGUMENTS. The rules come from [`notes/README.md`](../../../notes/README.md)
and [0005](../../../notes/0005-decision-log-filter.md); if this skill disagrees, they win.

## 1. Apply the filter

A note is warranted only when at least one of these holds:

- the decision constrains work beyond the change that implements it;
- it rejects an alternative that someone will plausibly propose again;
- it has no single commit to live in (process, architecture, spec interpretation,
  repository-wide tooling).

If none holds, say so, draft the commit body that carries the "why" instead, and stop.

## 2. Record only deliberation that happened

Take the alternatives from the conversation. If they are not there, ask the user which ones
were actually weighed and why they lost. Never invent a **Rejected** line to fill the
template: no alternative weighed means no **Rejected** section.

## 3. Number and name the file

Run `ls notes/`. The next number is the highest existing `NNNN-` plus 1, on 4 digits. The
slug is short and kebab-case: `notes/NNNN-short-slug.md`.

## 4. Write the note

Copy the shape of [`notes/0009-rbac-rules-from-markers-bindings-from-chart.md`](../../../notes/0009-rbac-rules-from-markers-bindings-from-chart.md):

- H1 `# NNNN — <title>` (the em dash is the notes' convention, keep it).
- `**Date:** YYYY-MM-DD · **Status:** decided`, with today's date in digits.
  Use `draft` if the user says the shape is not settled.
- `**Decision:**` first, stating the decision itself. One sentence, a few more only for
  the exceptions the reader must know.
- `**Why:**` in 2 to 4 bullets, each a fact the reader could not guess.
- `**Rejected:**` one bullet per alternative actually weighed: the alternative, a colon,
  why it lost.
- Under 25 lines in total. Follow the `## Writing` section of `AGENTS.md`: no padding, no
  restating the title.

Status values:

- `draft`: work in progress, may be incomplete or dropped; edit freely.
- `active`: the reference in force, details still expected to move; edit in place.
- `decided`: frozen; typo fixes only, changed only by a superseding note.
- `superseded by NNNN`: kept for history; read note `NNNN` instead.

How a note changes depends on its status ([0002](../../../notes/0002-development-pipeline.md),
working agreements):

- `draft`: edit freely, no trace needed.
- `active`: amend in place, the way 0002 and 0003 were:
  1. Set `**Amended:** YYYY-MM-DD` between Date and Status (add it, or update it to today).
  2. Open each changed passage with `*(Amended YYYY-MM-DD:)*`. When it reverses what the
     note first said, say so: `*(amendment vs the original plan, which ...)*`.
  3. Update the index line of `notes/README.md` if the decision in one line changed.
  4. When the shape stops moving, set the status to `decided`: the note is frozen from
     then on.
- `decided`: frozen, never rewritten; supersede it with a new note.

To supersede note `OOOO`:

1. Write the new note `NNNN` as above. Its **Decision** restates the whole decision now in
   force, not a diff, so the reader never needs `OOOO`; start it with
   `Supersedes [OOOO](./OOOO-slug.md).`
2. Its **Why** gives what changed since `OOOO`. The old decision becomes a **Rejected**
   line: it was weighed, and lost.
3. Supersede a whole note, never part of it. If only part of `OOOO` changes, the new note
   still restates the part that holds.
4. In `OOOO`, change only the status: `**Status:** superseded by [NNNN](./NNNN-slug.md)`.
   Date and body stay as they are.
5. In the index, keep the line of `OOOO` and end it with `Superseded by [NNNN](./NNNN-slug.md).`
6. Point the live references to `NNNN`: `grep -rn 'OOOO' AGENTS.md CONTRIBUTING.md .claude`.
   References inside other notes are history; leave them.

Skeleton:

```markdown
# NNNN — <title>

**Date:** YYYY-MM-DD · **Status:** decided

**Decision:** <the decision, in one sentence>.

**Why:**

- <reason the reader could not guess>
- <second reason>

**Rejected:**

- <alternative>: <why it lost>.
```

## 5. Add the index line

Append one entry to the `## Index` of `notes/README.md`, in number order, same shape as the
others: `- [NNNN — <title>](./NNNN-slug.md): <the decision in one line>.` State the
decision, not the topic. Wrap at about 90 columns with a 2-space continuation indent.

## 6. Link back

Remind the user that the commit implementing the decision references the note: the note
number in the commit body, and `[NNNN](./notes/NNNN-slug.md)` in `AGENTS.md` or the
matching `.claude/rules/` file when the decision constrains agents.

## 7. Lint

```sh
task lint-markdown-fix -- notes/NNNN-slug.md notes/README.md
```

Fix by hand what it could not fix, until `task lint-markdown -- <same files>` passes. Do not commit: the user reviews the note first.
