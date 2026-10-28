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
check refuse "git log | head"
check refuse "git log \$(id)"
check refuse "git -c core.pager=sh log"
check refuse "git push"
check refuse "task test-outline"
check pass "task test-outline -- internal/x" --outline
check refuse "task test-outline; ls" --outline
check refuse "task lint" --outline

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
