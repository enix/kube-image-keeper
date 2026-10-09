#!/usr/bin/env sh
# shellcheck disable=SC2016 # the commands under check are literal: nothing may expand
# Checks confirm-outbound-actions.sh on a table of commands: "ask" when the hook must ask the
# user, "pass" when it lets the command through. The hook only reads them, nothing runs. It
# works in a throwaway repository with a feature branch and origin set, so the result does
# not depend on the current branch or on the git config of the machine: task lint-hooks.
set -eu

hook="$(cd "$(dirname "$0")" && pwd)/confirm-outbound-actions.sh"
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
branch=feature/check
git -C "$repo" init -q -b "$branch"
git -C "$repo" remote add origin git@github.com:enix/kube-image-keeper.git
# A stub gh answers that the current branch has pull request 4242, with no network.
mkdir "$repo/bin"
printf '#!/bin/sh\n[ "$1 $2" = "pr view" ] && echo 4242\n' >"$repo/bin/gh"
chmod +x "$repo/bin/gh"
PATH="$repo/bin:$PATH"
failed=0

check() {
  want=$1
  cmd=$2
  out=$(jq -n --arg c "$cmd" --arg d "$repo" '{tool_input: {command: $c}, cwd: $d}' | sh "$hook")
  got=pass
  case "$out" in *'"ask"'*) got=ask ;; esac
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd"
    failed=1
  fi
}

# Its own pull request, without asking. The e2e-ready label goes through only on the pull
# request of the current branch.
check pass "gh pr edit 4242 --add-label e2e-ready"
check ask "gh pr edit 4243 --add-label e2e-ready"
check pass "git push origin $branch"
check pass "git push -u origin $branch"
check pass "git push --force-with-lease=$branch:0123abc origin $branch"
check pass "gh api graphql -f query='query { viewer { login } }' --jq '.data | .viewer'"
check pass "gh api graphql -f query='query { viewer { login } }'"
# Anything else that leaves the working copy still asks.
check ask "git push"
check ask "git push origin"
check ask "git push --force-with-lease"
check ask "git push origin main"
check ask "git push --force origin $branch"
check ask "git push -f"
check ask "git push origin $branch:main"
check ask "git push --tags"
check ask "git push origin v3.0.0"
check ask "git push origin other-branch"
check ask "git push origin $branch && echo done"
check ask "echo ok; git push"
check ask "gh pr merge 713 --rebase"
check ask "gh pr edit 713 --add-label other"
check ask "gh pr edit 713 --body x --add-label e2e-ready"
check ask "gh api graphql -f query='mutation { resolveReviewThread(input: {}) { thread { id } } }'"
# Quoting tricks that would hide a second command, a substitution or a mutation.
check ask 'gh api graphql -f query="$(git push origin main)"'
check ask 'gh api graphql -f query=`git push origin main`'
check ask "gh api graphql -f query=x \\'; git push origin main; echo \\'"
check ask "gh api graphql -f query='x'\"'\"'; git push origin main; '"
check ask "gh api graphql -f query='mu''tation { x }'"
check ask "gh api graphql -f query=\$'mutation { x }'"
check ask "gh api graphql -f query='query { x }' <(git push origin main)"
check ask "git push origin $branch >(git push origin main)"
check ask "git push origin $branch --force-with-lease=x\\';git push origin main;\\'"
check ask "git push origin $branch
git push origin main"
check ask "GIT_DIR=x git push origin $branch"
check ask "git -C .. push origin $branch"
check ask "git push origin +$branch"
check ask "git push --mirror origin $branch"
check ask "git push --delete origin $branch"
check ask "gh api graphql -F query=@q.graphql"
check ask "gh api graphql --input q.json"
check ask "gh api repos/enix/kube-image-keeper/issues/1/comments -f body=x"
check ask "task deploy"
check ask "kubectl apply -f x.yaml"
# A git config that would send the allowed push elsewhere, or with the tags.
git -C "$repo" config remote.origin.pushurl git@github.com:enix/kube-image-keeper.git
check pass "git push origin $branch"
git -C "$repo" config remote.origin.pushurl git@github.com:someone/else.git
check ask "git push origin $branch"
git -C "$repo" config --unset remote.origin.pushurl
git -C "$repo" config remote.origin.push "+refs/heads/$branch:refs/heads/main"
check ask "git push origin $branch"
git -C "$repo" config --unset remote.origin.push
git -C "$repo" config url.git@github.com:someone/.pushInsteadOf git@github.com:enix/
check ask "git push origin $branch"
git -C "$repo" config --remove-section url.git@github.com:someone/
git -C "$repo" config push.followTags true
check ask "git push origin $branch"
git -C "$repo" config --unset push.followTags
git -C "$repo" remote set-url origin git@github.com:someone/else.git
check ask "git push origin $branch"
git -C "$repo" remote set-url origin git@github.com:enix/kube-image-keeper.git
# A current branch that is main, a maintenance or release branch, or an option in disguise.
for other in main 2.3.x release-3.0 v3 --mirror; do
  git -C "$repo" symbolic-ref HEAD "refs/heads/$other"
  check ask "git push origin $other"
done
git -C "$repo" symbolic-ref HEAD "refs/heads/$branch"
check pass "git push origin $branch"
# Commands that never left the working copy are untouched.
check pass "git status"
check pass "git log --grep push"
check pass "gh pr view 713"

[ "$failed" -eq 0 ] && echo "all cases pass"
exit "$failed"
