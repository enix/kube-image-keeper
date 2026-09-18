# 0003 — how the work is driven

**Date:** 2026-08-21 · **Amended:** 2026-09-17 · **Status:** decided

Paul drives the v3 build himself, one task at a time, with an AI coding agent working in
front of him. Nothing runs unattended.

- Progress is tracked in one shared artefact: milestones, next steps, watch points and a
  journal, updated as tasks land. It is the working list; the notes here record only the
  decisions that outlive a task.
- For each task Paul says what to do, reviews the result and decides what is committed.
  He opens the pull requests and merges them. The agent never pushes, opens or merges
  anything on its own.
- Work lands on `main` of `enix/kube-image-keeper` through pull requests, from whatever
  branch Paul works on.
