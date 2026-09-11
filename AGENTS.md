# AGENTS.md

Guidance for AI coding agents working on this repository.

## What this branch is

kube-image-keeper (kuik) v3: a Kubernetes operator for container image routing,
mirroring and monitoring, rebuilt from a clean tree. The branch currently holds:

- [`docs/v3/`](./docs/v3/) — the specification, source of truth for behaviour. Read
  `spec.md` first, then `status.md`, the walkthroughs and `open-questions.md`.
- [`notes/`](./notes/) — the engineering journal: decisions, analyses, process. Start
  with [`notes/README.md`](./notes/README.md); its index states each decision in one
  line. Open a note only when it matters for the task, never the whole folder.
- `LICENSE`

Code, chart, CI and tooling are re-added milestone by milestone
([0002](./notes/0002-development-pipeline.md)). v2 code is retrieved from history with
`git show main:<path>` and lifted on purpose, never copied by reflex
([0001](./notes/0001-v2-reuse-analysis.md)).

## Writing

Everything written here is read under time pressure. Be concise and go straight to the
point, in commit messages, PR and issue descriptions, review comments, notes,
documentation, specification and answers to questions alike.

- **Lead with the answer**: the decision, the conclusion, what changed. Context comes
  after, and only if it changes what the reader does.
- **One idea per sentence.** Short and plain beats clause chains and jargon.
- **Cut what adds nothing**: restating the title, narrating the diff, announcing what you
  are about to say, closing summaries, filler adjectives.
- **Never pad to look thorough.** Length follows content, not effort.
- Concise is not incomplete: keep every fact the reader needs to act, drop the rest.

## Decision log

The "why" of a change lives in its commit body by default. Write a note under `notes/`
only when the decision passes the filter in [`notes/README.md`](./notes/README.md): it
constrains later work, rejects an alternative that will come back, or has no single
commit to live in. Notes record deliberation that actually happened; do not invent
alternatives to fill the template. Decision notes stay under 25 lines and are
append-only: supersede with a new note, never rewrite. A commit that implements a
decision references its note.
