#!/bin/sh
# Checks hack/check-commit-rules.sh in a throwaway repository: one branch per case on top of a
# base commit, and the exit code the script must give. Run by `task lint-hooks`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/check-commit-rules.sh
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git -C "$repo" init -q -b main
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q --allow-empty -m "chore: base"
failed=0

# commit <message> [author email]: an empty commit on the current branch.
commit() {
  git -C "$repo" -c user.name=check -c user.email="${2:-check@example.com}" commit -q --allow-empty -m "$1"
}

# check <expected exit code> <case>: the script on the commits of the current branch.
check() {
  set +e
  out=$(cd "$repo" && sh "$script" main 2>&1)
  code=$?
  set -e
  if [ "$code" != "$1" ]; then
    echo "FAIL: $2 exits $code, expected $1: $out"
    failed=1
  fi
  git -C "$repo" checkout -q main
}

branch() { git -C "$repo" checkout -q -b "$1" main; }

branch traced
commit "test(routing): add regression specs for the index"
commit "$(printf 'fix(routing): rebuild the index\n\nIntroduced-by: #742')"
check 0 "a fix with its trailer"

branch unknown
commit "$(printf 'fix: keep the lease\n\nThe origin is lost.\n\nIntroduced-by: unknown')"
check 0 "a fix whose origin is unknown"

branch untraced
commit "$(printf 'fix(routing): rebuild the index\n\nNo trailer here.')"
check 1 "a fix without the trailer"

branch notlast
commit "$(printf 'fix(routing): rebuild the index\n\nIntroduced-by: #742\nMore text after it.')"
check 1 "a fix whose trailer is not the last line"

branch badvalue
commit "$(printf 'fix(routing): rebuild the index\n\nIntroduced-by: 742')"
check 1 "a fix whose trailer names no pull request"

branch breaking
commit "$(printf 'fix(config)!: drop the old key\n\nIntroduced-by: #12')"
check 0 "a breaking fix with its trailer"

branch dependabot
commit "fix(deps): bump the patch group" "49699333+dependabot[bot]@users.noreply.github.com"
check 0 "a Dependabot fix(deps)"

branch fixup
commit "fixup! fix(routing): rebuild the index"
check 0 "a fixup! of a fix"

branch feat
commit "feat(routing): add the index"
check 0 "a feat needs no trailer"

branch tooling
commit "feat(claude): add a skill"
check 1 "a feat in the claude scope"

branch tooling-e2e
commit "$(printf 'fix(e2e): wait longer\n\nIntroduced-by: #1')"
check 1 "a fix in the e2e scope"

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
