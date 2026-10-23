---
name: release-notes
description: Use when preparing a kuik release from the beta on, before dispatching the Release workflow, or when asked to write or update the release notes of the next version, for example "write the notes of the next beta" or "prepare the release of 3.0.0".
argument-hint: "[branch, default main]"
allowed-tools: Bash(task release-dry-run *) Bash(task test-outline *) Bash(go run ./hack/releasenotes *) Bash(git fetch origin *) Bash(git log *) Bash(git show *) Bash(git ls-tree *) Bash(git merge-base *) Bash(git tag *) Bash(git describe *) Bash(git rev-list *) Bash(gh pr list *) Bash(gh pr view *) Bash(gh issue view *) Bash(gh release view *) Bash(git ls-remote *) Read Grep Glob Write
---

# Release notes

From the first beta on, every release carries notes written for the people who install kuik, not the list of commit subjects semantic-release generates. You write them as a structured file, `release-notes.yaml`, the single source: the release body and the chart's `artifacthub.io/changes` are rendered from it by `go run ./hack/releasenotes`. The person who releases decides, and the accepted notes go into a **draft GitHub release for exactly the tag of the next version**: the rendered body, and the YAML as its `release-notes.yaml` asset. The Release workflow refuses to release without that draft and a valid asset, and publishes the rendering of the asset in place of the generated notes, with the chart's changes rendered from it too: the draft's body is only a preview ([CONTRIBUTING](../../../CONTRIBUTING.md#release-notes)). Alphas keep the generated notes: this skill is not needed for them.

## Hard rules

- **The person decides**: show the whole rendered draft, apply their changes, and create nothing before they accept it.
- **Two outbound writes**: `gh release create ... --draft`, then `gh release upload` of the YAML, once accepted (the outbound hook asks, which is expected). Never publish a release, never create or push a tag, never dispatch the Release workflow.
- **The version is semantic-release's**, never computed by hand: a draft for another tag fails the workflow.
- **The YAML is the source**: every change goes into it, then render again. Never edit the rendered Markdown, and never edit a draft's notes on GitHub: the workflow ignores the body. Any change goes through this skill, which edits the YAML and replaces the draft's body and asset.
- English, plain words, short sentences ([AGENTS.md](../../../AGENTS.md#writing)).

## 1. The version

1. `task release-dry-run BRANCH=<branch>` (default `main`; `release` for a stable, once `release` is fast-forwarded to the commit to publish). It clones the branch from `origin` and runs semantic-release in dry-run with the workflow's plugins. It needs Node.js 22 or later and push access to `origin`. Its output is long: read only the line `The next release version is <version>`.
2. No release (no `feat`, `fix` or breaking change since the last one): say so and stop.
3. An alpha (`-alpha.` in the version): say the alpha keeps the generated notes, and stop.
4. Otherwise the tag is `v<version>`. Note the head of `origin/<branch>` (`git rev-list -1 origin/<branch>` after `git fetch origin`): the notes cover it.

## 2. What the release holds

The notes cover what changed since a baseline, `<previous>` below. Channels go by stability: alpha < beta < rc < stable. The baseline is the highest existing tag lower than the new version, in semver order, whose channel is at least as stable as the new version's. Take it among all tags, not only those merged into the branch: the `v2.3.x` stable tags live on the maintenance branch. For example:

- `3.0.0-beta.1`, `3.0.0-rc.1` and `3.0.0`: the latest `v2.3.x` stable.
- `3.0.0-beta.2`: `v3.0.0-beta.1`.
- `3.0.1`: `v3.0.0`.

List the candidates with `git tag --list 'v*' --sort=-v:refname`. Skip the tags higher than or equal to the new version, skip the less stable channels, and take the first one left. Git lists a pre-release above its own stable (`v2.3.1-rc.1` before `v2.3.1`): compare in semver order, where the stable is higher.

So the first release of a channel is a full summary for people coming from the previous stable, and the next ones of the same channel are incremental. When less stable pre-releases lie between the baseline and the new version (the alphas before `3.0.0-beta.1`, the betas before `3.0.0-rc.1` or `3.0.0`), still cover everything since the baseline: start from the written notes of those pre-releases (`gh release view <tag> --repo enix/kube-image-keeper`, or their `release-notes.yaml` asset), and from the commits for the alphas, whose notes are generated. Ask the person how far back to go when unsure.

The breaking changes serve a reader of each earlier pre-release too. For a change landed in an alpha, find the first tag that holds it with `git tag --contains <sha>`: its `from` is the release just before that first tag (a change first in `3.0.0-alpha.6` has `from: 3.0.0-alpha.5`, rendered "From `3.0.0-alpha.5` or earlier"). A break with the previous major has `from: v2`. One entry per requirement, each with its own `from`: cert-manager, required since the first v3 alpha, and Kubernetes 1.30, since `3.0.0-alpha.5`, are 2 entries.

semantic-release may compare from another tag (the first beta compares from `v2.3.0-beta.3`, the last beta on `main`'s channel): ignore that, the notes cover what changed since the baseline.

Read, in this order:

1. The commits: `git log --format='%h %s' <previous>..origin/<branch>`, and the bodies of the `feat`, `fix` and breaking ones.
2. The merged pull requests and what they close: `gh pr list --repo enix/kube-image-keeper --state merged --base <line> --limit 1000 --json number,title,body,mergeCommit,closingIssuesReferences`, kept when `mergeCommit.oid` is in `git rev-list <previous>..origin/<branch>` (the commits of the branch the baseline does not reach, even when it sits on another branch). `<line>` is the branch pull requests target: `main` for `main` and `release`, `X.Y.x` for `X.Y.x-stable` and `X.Y.x-rc`. Their `Upgrade notes:` and `Decisions:` lines ([pull-request skill](../pull-request/SKILL.md#2-draft-the-description)) are the main source of the breaking changes.
3. The issues they close: `gh issue view <n> --repo enix/kube-image-keeper`, for the user's words on what was missing.
4. The test outlines: `task test-outline DIFF=<previous>` lists the cases added and removed since the baseline, the behaviour as the specs pin it. It reads the working tree: run it where `HEAD` is `origin/<branch>`, or ask the person to. Skip it when the baseline is on another major line: every case shows as added and removed.
5. Where a change is unclear, the code at the head, `docs/`, or `docs/v3/`.

Leave out what users never see: refactors, test-only, docs-only, CI, tooling (`.claude/`, `hack/`, `test/acceptance/`) and dependency bumps without a user-visible effect. Keep a docs or chore commit only when it changes what a user meets (a default, a chart value, a metric, an image).

Leave out too a pull request or commit of the range whose code no longer exists at the head: the change is absent from the tree at the head (its files or symbols are gone, check with `git ls-tree` or `git show origin/<branch>:<path>`), or a later pull request removed it. After a major rewrite, the useful range starts at the pull request that replaced the tree: for the first beta of v3, the v2.4 work merged on `main` before the v3 rebuild was deleted by it.

## 3. The draft

Write the notes to `release-notes.yaml` in a directory outside the repository, in this shape ([the notes of 3.0.0-beta.1](../../../hack/releasenotes/testdata/3.0.0-beta.1.yaml) are a full example):

```yaml
version: 3.0.0-beta.2      # semantic-release's version, without the v
intro: >-                  # optional: what the release is, for whom, whether it is production-ready
  1 to 3 sentences.
highlights:                # 3 to 6, no references
  - lead: Short lead
    text: a highlight a user should remember, one line.
breaking:                  # what a user must change or will see differently on upgrade, and what to do
  - from: 3.0.0-beta.1     # or v2
    text: the value `a` is renamed `b`, see [the docs](https://kuik.enix.io/...).
    refs: {pulls: [801], closes: [790]}
limits:                    # optional: what does not work yet, and the workaround if any
  - area: Mirroring
    lead: Short lead
    text: it does not work yet, see [the docs](https://kuik.enix.io/...).
    refs: {external: [goharbor/harbor#24213]}   # optional here
features:
  - area: Routing
    text: One feature a user sees, in their words, one line.
    refs: {pulls: [802, 805]}
fixes:
  - area: Pull secrets
    text: One fix a user sees, in their words, one line.
    refs: {commits: [5fa90de9]}
```

Then render it from the repository root, and show the result:

```sh
go run ./hack/releasenotes -version <version> render <dir>/release-notes.yaml
go run ./hack/releasenotes -version <version> changes <dir>/release-notes.yaml
```

`render` prints the release body: the introduction, `## ✨ Highlights`, `## ⚠️ Breaking changes`, `## 🚧 Known limits`, and `## 📋 Changelog` last, its Features and Fixes collapsed and grouped under their areas. `changes` prints the chart's `artifacthub.io/changes`. Both refuse a file that breaks a rule and name it: fix the YAML and render again. With `-version`, as the workflow runs them, they also refuse notes whose `version` is another one. The tool enforces:

- **Strict fields**: an unknown or repeated field is an error.
- **Areas**: `Install`, `Routing`, `Pull secrets`, `Mirroring`, `Monitoring`, `Registries`, `Configuration`, `Observability`, `Chart`, the order users meet them. The entries of `limits`, `features` and `fixes` follow that order. The Changelog always shows the areas; Known limits shows them above 6 entries.
- **Highlights**: 3 to 6.
- **Texts**: one line each, no emoji (the 4 section headings carry the only ones), inline code allowed, Markdown links only in `breaking` and `limits`. A link must read well without its URL, since Artifact Hub shows only its text: `see the [Cluster ID section of the configuration docs](https://kuik.enix.io/...)`, not `([Cluster ID](https://kuik.enix.io/...))`.
- **`from`**: a major line (`v2`) or a version (`3.0.0-alpha.5`). "From `<version>` or earlier" includes the previous major line, so a reader coming from v2 is covered too.
- **`refs`**: `pulls` (the pull requests that delivered the change; several are fine when one change took several), `commits` (7 to 40 hex digits, only for a change without a pull request, never next to `pulls`), `closes` (the issues of this repository it closes) and `external` (`owner/repo#N`, an issue elsewhere). `breaking`, `features` and `fixes` need a pull request or a commit; `limits` may have none. They render at the end of the bullet as `(#802, closes #790)` and become the links of the Artifact Hub entry.

Write as follows, the tool cannot check it:

- **Breaking changes**: none is fine, the section then reads `None in this release.`. It also holds the upgrade steps: there is no separate upgrade notes section.
- **Fixes**: in the first release of a channel, counted from the previous stable, list only the fixes of behaviour that stable had: a fix of code that only existed in pre-releases is left out, its feature line covers it.
- **Length**: Highlights and Breaking changes stay short. The collapsed lists can be long, in the first release of a channel (a full summary since the previous stable) above all. Name the CRD kinds and values as users write them.
- No internal vocabulary (epics, specs, outlines).

Show the rendered body and the changes, with the version and the head they cover. Name what you left out and why in a few lines, so the person can bring something back. Apply their changes to the YAML and render again, until they accept.

## 4. The draft release

Once accepted, write the output of `render` to `<dir>/release-notes.md`, next to the YAML, and check no draft already exists for the tag (the workflow refuses several):

```sh
gh release view v<version> --repo enix/kube-image-keeper --json isDraft,body,assets
```

None: create it, then attach the YAML. The asset takes the file's name, and the workflow looks for exactly `release-notes.yaml`.

```sh
gh release create v<version> --repo enix/kube-image-keeper --draft --title v<version> --notes-file <dir>/release-notes.md
gh release upload v<version> <dir>/release-notes.yaml --repo enix/kube-image-keeper
```

A draft (`isDraft` true): show the difference and, once the person agrees, replace both: its body with `gh release edit v<version> --repo enix/kube-image-keeper --notes-file <dir>/release-notes.md`, its asset with `gh release upload v<version> <dir>/release-notes.yaml --repo enix/kube-image-keeper --clobber`. A published release (`isDraft` false): the version is already out, so stop and tell the person; never edit it.

A draft creates no tag: semantic-release creates it when the workflow runs, and the workflow deletes the draft once its notes are taken. Check the asset: `gh release view v<version> --repo enix/kube-image-keeper --json assets` lists `release-notes.yaml`. Check the tag: `git ls-remote --tags origin v<version>` must print nothing. If it prints the tag, stop and tell the person: the tag must not exist before the release, and the workflow would compute another version. Then tell the person:

- the draft's URL;
- that the notes cover `origin/<branch>` at the head noted in step 1: if commits land before the dispatch, run the skill again (a new commit may change the version, and the workflow then fails naming both);
- that the draft must not change once the workflow runs, and that before it, any change goes through this skill, never through GitHub's editor: the workflow publishes the asset's rendering, not the body;
- that the next step is theirs: dispatch the Release workflow on `<branch>`.
