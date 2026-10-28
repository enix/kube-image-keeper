#!/usr/bin/env sh
# PreToolUse hook (Bash) of the read-only review agents (reviewer, spec-reviewer): Bash runs only
# git diff, show, log and grep, in the working copy or with -C on a worktree of this repository
# under .claude/worktrees/, and with --outline also task test-outline. No shell operator,
# redirection, substitution or escape, no option that writes a file or runs a program
# (--output, --no-index, grep -O / --open-files-in-pager). It refuses (exit 2): the agent stops.
set -eu

refuse() {
  echo "$1 is read-only: Bash runs only git diff, git show, git log and git grep (git -C <worktree under .claude/worktrees/> included)${2:-}, without shell operators, --output, --no-index or grep -O" >&2
  exit 2
}

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
agent=reviewer
outline=
if [ "${1:-}" = --outline ]; then
  agent=spec-reviewer
  outline=", and task test-outline"
fi
input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')

# No shell operator, redirection, substitution, escape or second line.
case "$cmd" in
  *[\;\&\|\<\>\`\$\\]* | *'
'*) refuse "$agent" "$outline" ;;
esac
# No option that writes a file, reads outside the repository or runs a program.
if printf '%s' "$cmd" | grep -Eq -- '--output|--no-index|--open-files-in-pager|(^|[[:space:]])-[A-Za-z]*O'; then
  refuse "$agent" "$outline"
fi

if [ -n "$outline" ] && printf '%s' "$cmd" | grep -Eq '^task[[:space:]]+test-outline([[:space:]]|$)'; then
  exit 0
fi
printf '%s' "$cmd" | grep -Eq '^git[[:space:]]+(diff|show|log|grep)([[:space:]]|$)' && exit 0

# git -C <worktree>: the path must be the top of a worktree of this repository, listed by git
# worktree list, under the main worktree's .claude/worktrees/, with no `..` or symlink on the way.
printf '%s' "$cmd" | grep -Eq '^git[[:space:]]+-C[[:space:]]+[A-Za-z0-9._/-]+[[:space:]]+(diff|show|log|grep)([[:space:]]|$)' || refuse "$agent" "$outline"
dir=$(printf '%s' "$cmd" | sed -E 's/^git[[:space:]]+-C[[:space:]]+([A-Za-z0-9._/-]+).*/\1/')
case "$dir" in .. | ../* | */.. | */../*) refuse "$agent" "$outline" ;; esac
start=$(cd "${cwd:-.}" 2>/dev/null && pwd -P) || refuse "$agent" "$outline"
case "$dir" in /*) d=$dir ;; *) d=$start/$dir ;; esac
logical=$(CDPATH='' cd "$d" 2>/dev/null && pwd -L) || refuse "$agent" "$outline"
physical=$(CDPATH='' cd "$d" 2>/dev/null && pwd -P) || refuse "$agent" "$outline"
[ "$logical" = "$physical" ] || refuse "$agent" "$outline"
listed=$(git -C "$start" worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p') || refuse "$agent" "$outline"
main=$(printf '%s\n' "$listed" | head -n 1)
[ -n "$main" ] || refuse "$agent" "$outline"
main=$(cd "$main" 2>/dev/null && pwd -P) || refuse "$agent" "$outline"
case "$physical" in "$main"/.claude/worktrees/?*) ;; *) refuse "$agent" "$outline" ;; esac
printf '%s\n' "$listed" | while IFS= read -r w; do
  [ -n "$w" ] && [ "$(cd "$w" 2>/dev/null && pwd -P)" = "$physical" ] && echo found && break
done | grep -q found || refuse "$agent" "$outline"
exit 0
