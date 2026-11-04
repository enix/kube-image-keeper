#!/bin/sh
# Runs a hook of .claude/hooks as it stands on origin/main, whatever the branch of the session:
# a session started in an old worktree would otherwise run that worktree's old hooks, and a
# branch could weaken the guard it is reviewed under by editing it.
# Usage: run-main-hook.sh <hook file> [args...], with the tool call on stdin.
# KUIK_LIVE_HOOKS=1 runs the working copy's hook instead, to try a hook being changed. Without a
# local origin/main (a fresh clone not fetched yet), the working copy's hook runs, as before.
# The hooks of each origin/main commit are extracted once, under the git directory shared by every
# worktree, so a call costs one git rev-parse.
hook=$1
shift
here=$(cd "$(dirname "$0")" && pwd)
live() { exec "$here/$hook" "$@"; }

case "$hook" in '' | */* | .*) echo "run-main-hook: bad hook name: $hook" >&2; exit 2 ;; esac
[ "${KUIK_LIVE_HOOKS:-}" = 1 ] && live "$@"

sha=$(git -C "$here" rev-parse --verify --quiet 'refs/remotes/origin/main^{commit}' 2>/dev/null) || live "$@"
common=$(git -C "$here" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || live "$@"
dir="$common/kuik-main-hooks/$sha"
if [ ! -x "$dir/.claude/hooks/$hook" ]; then
  # A hook that does not exist on origin/main yet runs from the working copy.
  git -C "$here" cat-file -e "$sha:.claude/hooks/$hook" 2>/dev/null || live "$@"
  tmp=$(mktemp -d "$common/kuik-main-hooks.XXXXXX") || live "$@"
  # Run from the hooks directory, git archive would keep only that directory of the tree.
  if git --git-dir="$common" archive --prefix=.claude/hooks/ "$sha:.claude/hooks" | tar -x -C "$tmp" 2>/dev/null; then
    mkdir -p "$common/kuik-main-hooks"
    # Another call may have extracted the same commit meanwhile: either copy is the same.
    mv "$tmp" "$dir" 2>/dev/null || rm -rf "$tmp"
  else
    rm -rf "$tmp"
    live "$@"
  fi
fi
# A copy left incomplete by an interrupted extraction is never run.
[ -x "$dir/.claude/hooks/$hook" ] || live "$@"
exec "$dir/.claude/hooks/$hook" "$@"
