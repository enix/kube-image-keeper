#!/usr/bin/env sh
# Checks review-guard.sh on a table of commands, in a throwaway repository with a worktree under
# .claude/worktrees/: "pass" when the review agents may run the command, "refuse" otherwise.
# The hook only reads them: task lint-hooks.
set -eu

hook="$(cd "$(dirname "$0")" && pwd)/review-guard.sh"
repo=$(cd "$(mktemp -d)" && pwd -P)
outside=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$repo" "$outside"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git -C "$repo" init -q -b main
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q --allow-empty -m main
wt="$repo/.claude/worktrees/topic"
git -C "$repo" worktree add -q --detach "$wt"
mkdir -p "$repo/.claude/worktrees/plain"
ln -s "$wt" "$repo/.claude/worktrees/link"
failed=0

check() {
  want=$1
  cmd=$2
  got=pass
  jq -n --arg c "$cmd" --arg d "$repo" '{tool_input: {command: $c}, cwd: $d}' | sh "$hook" ${3:-} 2>/dev/null || got=refuse
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd ${3:-}"
    failed=1
  fi
}

check pass "git diff origin/main...HEAD"
check pass "git log --oneline -5"
check pass "git show HEAD --stat"
check pass "git grep -n foo"
check pass "git -C $wt diff origin/main...HEAD"
check pass "git -C $wt log --stat"
check pass "git -C $wt grep foo"
check pass "git -C .claude/worktrees/topic show HEAD"
check refuse "git -C /tmp diff"
check refuse "git -C $outside diff"
check refuse "git -C $repo diff"
check refuse "git -C $repo/.claude/worktrees/plain diff"
check refuse "git -C $repo/.claude/worktrees/link diff"
check refuse "git -C $wt/../topic diff"
check refuse "git grep -O vim foo"
check refuse "git grep -nOvim foo"
check refuse "git grep --open-files-in-pager=sh foo"
check refuse "git -C $wt diff; ls"
check refuse "git -C $wt log --output=/tmp/x"
check refuse "git diff --no-index /etc/passwd x"
# A path outside the repository turns git diff into --no-index on its own.
check refuse "git diff /etc/passwd /dev/null"
check refuse "git diff ../secret x"
check refuse "git diff ~/x y"
check refuse "git -C $wt diff /etc/passwd /dev/null"
check refuse "git diff '/etc/passwd' /dev/null"
# The shell drops quotes before git reads an option; an external diff or a textconv driver runs
# a program.
check refuse "git diff --out\"put\"=review.patch"
check refuse "git diff --textconv -- asset.dat"
check refuse "git log -p --ext-diff"
check pass "git log --format='%h %s'"
# git takes a unique prefix of a long option, and a short option anywhere in a cluster.
check refuse "git grep --open-files-in-page=sh foo ."
check refuse "git diff --out=review.patch"
check refuse "git grep -1Osh foo ."
check refuse "git grep -f/tmp/patterns -- README.md"
check refuse "git grep -f /tmp/patterns -- README.md"
check refuse "git grep --file=/tmp/patterns"
check pass "git log --oneline --stat -5"
check pass "git grep -n -e foo -- internal"
check pass "git diff --no-color --name-only"
check pass "git diff main..HEAD"
check pass "git diff ..HEAD -- internal/x"
check refuse "git log | head"
check refuse "git log \$(id)"
check refuse "git -c core.pager=sh log"
check refuse "git push"
check refuse "task test-outline"
check pass "task test-outline -- internal/x" --outline
check refuse "task test-outline; ls" --outline
check refuse "task lint" --outline
# test-outline alone: no second task and no option for Task itself.
check refuse "task test-outline lint-hooks" --outline
check refuse "task test-outline --taskfile x.yaml" --outline
check pass "task test-outline DIFF=origin/main -- internal/x" --outline
check pass "task test-outline" --outline

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
