---
name: epic
description: Use when an epic of kuik starts and needs its GitHub issue, or when a pull request of an epic opens or merges; drafts the epic issue for the user to confirm, opens it, and keeps its pull request checklist in step.
argument-hint: "[what the epic delivers, or an epic issue number]"
allowed-tools: Bash(gh issue view *) Bash(gh issue list *) Bash(gh pr view *) Bash(git grep *) Bash(git show *) Read Grep Glob Write
---

# Epic issue

Target: `$ARGUMENTS` (what the epic delivers, or the number of an existing epic issue).
Every step that writes on GitHub waits for the user's explicit go: show the text first,
then run the command.

## Vocabulary

- **Milestones** are the GitHub milestones `v3.0 beta` (every feature implemented) and
  `v3.0 GA` (stable: load, scale, e2e coverage, hardening). There is no other.
- An **epic** groups related features delivered by several pull requests. Every epic gets
  an issue when it starts.
- Its pull-request-sized pieces are **issues**. For now they stay checkboxes in their epic:
  open no plain issue.

## 1. Title, labels, milestone

- Title: what the epic delivers, in plain English, as a verb phrase ("Monitor the images
  the cluster runs with ImageMonitor"). No `Epic:` prefix: the label says it.
- Labels: `epic`, `enhancement`, `v3`.
- Milestone: `v3.0 beta` for a feature, `v3.0 GA` for stabilisation (scale, e2e coverage,
  hardening). When unsure, ask the user, with your recommendation.

## 2. Body

Follow [#718](https://github.com/enix/kube-image-keeper/issues/718) (several pull
requests, known limits) and [#722](https://github.com/enix/kube-image-keeper/issues/722)
(acceptance criteria):

```markdown
<What the epic delivers and why, 2 to 4 sentences.>

Spec: <one link per docs/v3 heading, comma-separated>.

## Pull requests

- [ ] **<Pull request title>**: <one line on what it brings>.
- [ ] **<Pull request title>**
  - <acceptance criterion>
  - <...>

<Optional: one sentence on ordering or dependencies.>

## Known limits

- <What the epic deliberately leaves out, or a constraint it cannot lift.>

## Done when

<The pull requests merged, with their specs, e2e and docs, and the observable outcome.>
```

- The `Spec:` line links the `docs/v3/` headings by absolute URL on `main`
  (`https://github.com/enix/kube-image-keeper/blob/main/docs/v3/<file>.md#<heading-slug>`):
  an issue has no relative path. Check that each heading exists. Drop the line when no spec
  section applies.
- One checkbox per pull request. More than 3 acceptance criteria: a sub-list of criteria
  instead of the line.
- `## Known limits` only when there are some.

## 3. Writing

- Public wording only: no internal ids (epic numbers like M3b, amendment or lesson ids), no
  link to a private page.
- No hard line break inside a paragraph or a list item: GitHub keeps them. One line per
  paragraph or item.
- Security topics state the hardening goals, never how to exploit the current weakness.
- Follow the `## Writing` section of `AGENTS.md`: lead with what it delivers, cut the rest.

## 4. Open it

Show the user the title, labels, milestone and body, and wait. Apply their changes, show
again, until they confirm. Then write the title and the body to files (a title often holds
an apostrophe, which breaks a quoted argument) and run, alone:

```sh
gh issue create --title "$(cat <title-file>)" --label epic,enhancement,v3 --milestone 'v3.0 beta' --body-file <body-file>
```

Use `--milestone 'v3.0 GA'` for a stabilisation epic.

## 5. Keep it in step

- When a pull request of the epic opens, add its number to its checkbox
  (`**<title>** (#N)`). Its description must say `Part of #<epic>`: when it does not, draft
  the edited description, show it, and apply it with `gh pr edit <n> --body-file <file>`.
- When it merges, tick its box. Edit with `gh issue edit <epic> --body-file <file>`, after
  showing the change.
- A pull request added, split or dropped changes the checklist the same way.
