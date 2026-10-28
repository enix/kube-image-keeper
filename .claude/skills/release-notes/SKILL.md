---
name: release-notes
description: Use when preparing any kuik release, alphas included, before dispatching the Release workflow, or when asked to write or update the release notes of the next version, for example "write the notes of the next beta" or "prepare the release of 3.0.0".
argument-hint: "[branch, default main]"
allowed-tools: Bash(task release-dry-run *) Bash(task test-outline *) Bash(go run ./hack/releasenotes *) Bash(git fetch origin *) Bash(git log *) Bash(git show *) Bash(git ls-tree *) Bash(git merge-base *) Bash(git tag *) Bash(git describe *) Bash(git rev-list *) Bash(gh pr list *) Bash(gh pr view *) Bash(gh issue view *) Bash(gh release view *) Bash(git ls-remote *) Read Grep Glob Write
---

# Release notes

Every release carries notes written for the people who install kuik, not the list of commit subjects semantic-release generates. You write them as a structured file, `release-notes.yaml`, the single source: the release body and the chart's `artifacthub.io/changes` are rendered from it by `go run ./hack/releasenotes`. The person who releases decides, and the accepted notes go into a **draft GitHub release for exactly the tag of the next version**: the rendered body, and the YAML as its `release-notes.yaml` asset. The Release workflow refuses to release without that draft and a valid asset, and publishes the rendering of the asset in place of the generated notes, with the chart's changes rendered from it too: the draft's body is only a preview ([CONTRIBUTING](../../../CONTRIBUTING.md#release-notes)).

## Hard rules

- **The person decides**: show the whole rendered draft, apply their changes, and create nothing before they accept it.
- **Two outbound writes**: `gh release create ... --draft`, then `gh release upload` of the YAML, once accepted (the outbound hook asks, which is expected). Never publish a release, never create or push a tag, never dispatch the Release workflow.
- **The version is semantic-release's**, never computed by hand: a draft for another tag fails the workflow.
- **The YAML is the source**: every change goes into it, then render again. Never edit the rendered Markdown, and never edit a draft's notes on GitHub: the workflow ignores the body. Any change goes through this skill, which edits the YAML and replaces the draft's body and asset.
- English, plain words, short sentences ([AGENTS.md](../../../AGENTS.md#writing)).

## 1. The version

1. `task release-dry-run BRANCH=<branch>` (default `main`; `release` for a stable, once `release` is fast-forwarded to the commit to publish). It clones the branch from `origin` and runs semantic-release in dry-run with the workflow's plugins. It needs Node.js ^22.14.0 or >= 24.10.0 (semantic-release 25) and push access to `origin`. Its output is long: read only the line `The next release version is <version>`.
2. No release (no `feat`, `fix` or breaking change since the last one): say so and stop.
3. Otherwise the tag is `v<version>`. Note the head of `origin/<branch>` (`git rev-list -1 origin/<branch>` after `git fetch origin`): the notes cover it.

## 2. What the release holds

The notes are written for the readers of the new version's stability: an alpha for people who run alphas, a beta for people who run betas, a stable for people who run stables. They cover what changed since a baseline, `<previous>` below, which the YAML names as `since`. Stability goes alpha < beta < rc < stable. The baseline is the latest version lower than the new one, at least as stable, on the same line (`X.Y`); on another line, only stables count. Take it among all tags, not only those merged into the branch: the `v2.3.x` stable tags live on the maintenance branch. For example:

- `3.0.0-beta.1`, `3.0.0-rc.1` and `3.0.0`: `2.3.1`, the latest stable, since no version of the 3.0 line is at least as stable.
- `3.0.0-beta.2`: `3.0.0-beta.1`.
- `3.0.1-rc.1` and `3.0.1`: `3.0.0` (`3.0.0-rc.1` is lower than `3.0.0`).
- `3.1.0-alpha.1`: the latest `3.0.x` stable. `3.1.0-alpha.2`: `3.1.0-alpha.1`.

List the candidates with `git tag --list 'v*' --sort=-v:refname`. Skip the tags higher than or equal to the new version and the less stable ones, and take the first one left on the same line, else the first stable. Git lists a pre-release above its own stable (`v2.3.1-rc.1` before `v2.3.1`): compare in semver order, where the stable is higher.

So the first release of each stability level on a line (`3.1.0-alpha.1`, `3.0.0-beta.1`, `3.0.0-rc.1`, `3.0.0`, or a stable whose line skipped levels) is a full summary since the previous stable: every feature, every breaking change with its `from`, and the fixes of bugs its readers could meet (see Fixes below). The next releases of that level cover the changes since their baseline. When less stable pre-releases lie between the baseline and the new version (the alphas before `3.0.0-beta.1`, the betas before `3.0.0-rc.1` or `3.0.0`), still cover everything since the baseline: start from the written notes of those pre-releases (`gh release view <tag> --repo enix/kube-image-keeper`, or their `release-notes.yaml` asset), and from the commits for the alphas up to `3.0.0-alpha.6`, whose notes were generated. Ask the person how far back to go when unsure.

The first release of a stability level has a second reader: someone upgrading from the version directly before it, whatever its level (the last alpha for `3.0.0-beta.1`, the last beta for `3.0.0-rc.1`, the last rc for a stable). Enix runs every version and nobody skips a level. The YAML's `previous` serves them: `previous.version` is that version, and its `features` and `fixes` are every feature and every fix between it and the new version, fixes of bugs only pre-releases had included. They do not show in the release body, whose Changelog counts from `since` only: they become the release's entries on Artifact Hub, and the breaking changes `from: <previous.version>` show in the body. The release's `CHANGELOG.md` asset gives developers the commit-level delta since the version directly before. The other releases have no `previous`, nor has a first alpha whose version directly before is the stable it counts from.

The breaking changes serve these readers only: the one coming from `since` and, with `previous`, the one coming from `previous.version`. The `from` of a change is the reader it breaks: `since`, or its major line (`v2`) when `since` is on an earlier major, for the main reader; `previous.version` for the second. `from` only picks where the change shows: the section groups the changes per reader only when something breaks the second reader; otherwise it has plain bullets. A change that breaks both readers is written twice, once per `from`, so each finds it in their group. A change that broke only what intermediate pre-releases had, which both readers already have or never had, is not listed; the tool refuses any other `from` and names the allowed ones. One entry per requirement: cert-manager and Kubernetes 1.30 are 2 entries. A behaviour change in a patch release is a breaking change too, with `from` the previous stable.

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

First check [`docs/known-limits.md`](../../../docs/known-limits.md) at the head is up to date with the release's changes: every limit a change of the range adds is in it, every limit it lifts is gone. The notes have no Known limits section: that page holds the full list for the major line, read in its latest state. If it is out of date, stop and tell the person: it is fixed in a pull request before the release.

Write the notes to `release-notes.yaml` in a directory outside the repository, in this shape ([the notes of 3.0.0-beta.1](../../../hack/releasenotes/testdata/3.0.0-beta.1.yaml) are a full example):

```yaml
version: 3.0.0-beta.2      # semantic-release's version, without the v
since: 3.0.0-beta.1        # the baseline of step 2, without the v
intro: >-                  # optional: what the release is, for whom, whether it is production-ready
  1 to 3 sentences.
highlights:                # 0 to 5, no references
  - lead: Short lead
    text: a highlight a user should remember, one line.
breaking:                  # what a user must change or will see differently on upgrade, and what to do
  - from: 3.0.0-beta.1     # the reader it breaks: since, its major line (v2) on an earlier major, or previous.version
    text: The value `a` is renamed `b`, see [the docs](https://kuik.enix.io/...).
    refs: {pulls: [801], closes: [790]}
features:
  - area: Routing
    text: One feature a user sees, in their words, one line.
    refs: {pulls: [802, 805]}
fixes:
  - area: Pull secrets
    text: One fix a user sees, in their words, one line.
    refs: {commits: [5fa90de9]}
previous:                  # only in the first release of a stability level (X.Y.Z or -<channel>.1)
  version: 3.0.0-alpha.9   # the version directly before, without the v
  features: []             # same entries as features
  fixes: []                # same entries as fixes
```

Then render it from the repository root, and show the result:

```sh
go run ./hack/releasenotes -version <version> render <dir>/release-notes.yaml
go run ./hack/releasenotes -version <version> changes <dir>/release-notes.yaml
```

`render` prints the release body: the introduction, `## ✨ Highlights`, `## ⚠️ Breaking changes`, and `## 📋 Changelog` last, its Features and Fixes collapsed and grouped by area: the entries of an area with a single one come first, in one list, each led by its area (`- **Routing**: ...`), then each area with several entries under its bold line, in area order. The Changelog always opens with a line linking the `since` release ("Changes since 2.3.1.") and holds that single comparison: `previous.features` and `previous.fixes` never render in the body. When the notes have `previous` and at least one breaking change `from: <previous.version>`, the Breaking changes are grouped under `### From <reader>`: the main reader first (`since`, or its major line `v2` when `since` is on an earlier major), reading "Nothing breaks since <since>." when nothing breaks it, then `previous.version`. Otherwise they are plain bullets. Without highlights, their section is left out. Without breaking changes, their section is left out and **No breaking change in this release.** follows the introduction as its own bold paragraph (first when there is no `intro`). `changes` prints the chart's `artifacthub.io/changes`: Artifact Hub stacks every version in one view, so its entries must read as successive deltas. Without `previous`, they are the notes' main lists. With `previous`, stable or not, they are the second reader's lists instead: the breaking changes from `previous.version`, `previous.features` and `previous.fixes`, so the full summary of a first release of a level does not repeat what earlier versions listed. Both refuse a file that breaks a rule and name it: fix the YAML and render again. With `-version`, as the workflow runs them, they also refuse notes whose `version` is another one. The tool enforces:

- **Strict fields**: an unknown or repeated field is an error.
- **Areas**: `Install`, `Routing`, `Pull secrets`, `Mirroring`, `Monitoring`, `Registries`, `Configuration`, `Observability`, `Chart`, the order users meet them. The entries of `features` and `fixes` follow that order, and the Changelog shows the areas.
- **`since`**: required, a version below the notes' own.
- **`previous`**: only when the notes' version is a stable `X.Y.Z` or a pre-release numbered 1; its `version` is required, below the notes' own and above `since`; its entries follow the rules of `features` and `fixes`.
- **Highlights**: 0 to 5. None suits a release of a few fixes.
- **Texts**: one line each, no emoji (the 3 section headings carry the only ones), inline code allowed, Markdown links only in `breaking`. A link must read well without its URL, since Artifact Hub shows only its text: `see the [Cluster ID section of the configuration docs](https://kuik.enix.io/...)`, not `([Cluster ID](https://kuik.enix.io/...))`.
- **`from`**: `since`, its major line (`v2`) when `since` is on an earlier major, or `previous.version` (only with `previous`).
- **`refs`**: `pulls` (the pull requests that delivered the change; several are fine when one change took several), `commits` (7 to 40 hex digits, only for a change without a pull request, never next to `pulls`), `closes` (the issues of this repository it closes) and `external` (`owner/repo#N`, an issue elsewhere). Every entry needs a pull request or a commit. They render at the end of the bullet as `(#802, closes #790)` and become the links of the Artifact Hub entry.

Write as follows, the tool cannot check it:

- **Intro**: state a stability warning (such as "keep v2.3 for production") only where readers need it: never in an alpha, and in a beta or an rc only in the first release of its level (`X.Y.Z-beta.1`, which also says the release is feature complete, or `X.Y.Z-rc.1`). The later releases of a level do not repeat it.
- **Breaking changes**: none is fine, the tool then leaves the section out and writes **No breaking change in this release.** as a bold paragraph after the introduction. It also holds the upgrade steps: there is no separate upgrade notes section. Each text is a bullet of its own, with no lead: start it with a capital.
- **Known limits**: the notes carry only what changes: a limit the release lifts is a feature or a fix in the Changelog; a limit it adds is a breaking change (something that worked no longer does) or is said in the entry of the new feature.
- **Fixes**: list a fix in the main lists only when its reader could have run the bug: the commit that introduced it is in `since`, or in a released version of the new version's stability level between `since` and the new version. Find that commit from the fix's `Introduced-by: #<PR>` line when it has one (the merge commit of that pull request), otherwise from `git log`, `git blame` and the code, then list the releases that hold it with `git tag --contains <commit> --sort=v:refname`. `Introduced-by: unknown`, or an older `fix` commit without the line: decide from its body and the code, and name it among what you kept or left out. A bug of another version's pre-release counts only through the stable that still carried it: a bug of `2.3.0-rc.1` still in `2.3.1` counts for a reader of `2.3.1`. Leave out a bug that had no visible effect in the versions that carried it (its code was not reachable yet), and a bug only less stable pre-releases had: the feature line covers it. The text of a kept fix of a kuik bug ends with `(broken since <version>)`, before the references, `<version>` being the first version of the new version's stability level that had the bug; a bug inherited from the line of `since` names that line's first version with it (`A copy no longer stalls on a 429 (broken since 2.3.0).`). A security `fix(deps)` is a fix entry with no `(broken since <version>)`: its text names the vulnerability it closes, its CVE or advisory id and the affected dependency. A dependency update that closes no vulnerability reachable in kuik is not a fix and stays out of the notes. `previous.fixes` lists every fix since the version directly before, with no "broken since".
- **Length**: Highlights and Breaking changes stay short. The collapsed lists can be long, in the first release of a stability level (a full summary since the previous stable) above all. Name the CRD kinds and values as users write them.
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

A draft creates no tag: semantic-release creates it when the workflow runs, and the workflow deletes the draft once its notes are taken. The published release carries the YAML as its `release-notes.yaml` asset, next to `CHANGELOG.md`, the changelog semantic-release generates from the commits. Check the asset: `gh release view v<version> --repo enix/kube-image-keeper --json assets` lists `release-notes.yaml`. Check the tag: `git ls-remote --tags origin v<version>` must print nothing. If it prints the tag, stop and tell the person: the tag must not exist before the release, and the workflow would compute another version. Then tell the person:

- the draft's URL;
- that the notes cover `origin/<branch>` at the head noted in step 1: if commits land before the dispatch, run the skill again (a new commit may change the version, and the workflow then fails naming both);
- that the draft must not change once the workflow runs, and that before it, any change goes through this skill, never through GitHub's editor: the workflow publishes the asset's rendering, not the body;
- that the next step is theirs: dispatch the Release workflow on `<branch>`.
