#!/usr/bin/env sh
# shellcheck disable=SC2016 # the commands under check are literal: nothing may expand
# Checks confirm-history-rewrites.sh on a table of commands: "ask" when the hook must ask the
# user, "pass" when it lets the command through. The hook only reads them, nothing runs. It
# works in a throwaway directory holding one file, so `git reset <path>` has a path to find:
# task lint-hooks.
set -eu

hook="$(cd "$(dirname "$0")" && pwd)/confirm-history-rewrites.sh"
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
touch "$dir/go.mod"
failed=0

check() {
  want=$1
  cmd=$2
  out=$(jq -n --arg c "$cmd" --arg d "$dir" '{tool_input: {command: $c}, cwd: $d}' | sh "$hook")
  got=pass
  case "$out" in *'"ask"'*) got=ask ;; esac
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd"
    failed=1
  fi
}

# Starting a rebase asks, in every form, the autosquash of the pull-request skill included.
check ask "git rebase origin/main"
check ask "git rebase feature/other"
check ask "git -C ../repo rebase main"
check ask "GIT_SEQUENCE_EDITOR=: git rebase -i --autosquash 0123abc"
check ask "git rebase --onto main old"
check ask "git pull --rebase"
check ask "git pull -r origin main"
check ask "echo ok && git rebase main"
# The steps of a rebase already started pass.
check pass "git rebase --continue"
check pass "git add . && git rebase --continue"
check pass "GIT_EDITOR=true git rebase --continue"
check pass "git rebase --skip"
check pass "git rebase --abort"
check pass "git rebase --quit"
# Amending asks; a fixup commit passes.
check ask "git commit --amend --no-edit"
check ask "git commit -a --amend"
check pass "git commit --fixup=0123abc"
check pass "git commit -m 'fix(registry): retry on 429'"
# git reset moves the branch with a mode or a commit-ish alone; a path only unstages.
check ask "git reset --hard origin/main"
check ask "git reset --soft HEAD~1"
check ask "git reset HEAD~2"
check ask "git reset 0123abc"
check ask "git reset main"
check pass "git reset go.mod"
check pass "git reset -- go.mod"
check pass "git reset HEAD go.mod"
check pass "git reset"
# Moving a branch asks; creating one passes.
check ask "git branch -f main HEAD~1"
check ask "git branch -M old new"
check ask "git checkout -B topic"
check ask "git switch -C topic"
check ask "git update-ref refs/heads/main 0123abc"
check ask "git filter-branch --tree-filter true"
check pass "git branch topic"
check pass "git switch -c topic"
check pass "git checkout topic"
# Reading the history passes.
check pass "git log --oneline origin/main..HEAD"
check pass "git range-diff ORIG_HEAD...HEAD"

[ "$failed" -eq 0 ] && echo "all cases pass"
exit "$failed"
