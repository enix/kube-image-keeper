# 0005 — notes only for decisions that outlive their commit

**Date:** 2026-09-11 · **Status:** decided

**Decision:** a decision's rationale lives in the commit body by default. A note is written
only when the decision constrains work beyond its own change, rejects an alternative that
will plausibly come back, or has no single commit to live in. Decision notes stay under
25 lines, the index states each decision in one line, notes are read on demand. Replaces
the "every decision gets a note" rule of `AGENTS.md` on `main` (`977d0ec`).

**Why:**

- The old rule turned the `v3` branch creation into a 57-line note whose "rejected
  alternatives" were invented to fill the template; fabricated deliberation is worse than none.
- Notes are read by agents; at that density the folder costs tens of thousands of tokens
  before the first milestone lands.
- `git log` is already append-only and searchable; a note only adds what a commit cannot.

**Rejected:**

- Several tiers of notes (decisions vs journal): a taxonomy to argue about every time, for
  what a filter plus the commit body already give.
- Keeping the maximalist rule and trimming later: pollution compounds and invented
  rationale stays in history.
