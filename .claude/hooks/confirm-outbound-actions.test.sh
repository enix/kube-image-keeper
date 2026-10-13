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
# A stub gh answers that the current branch has pull request 4242, with no network, and
# whether auto-merge is off on it: "true" unless AUTOMERGE_OFF says otherwise.
mkdir "$repo/bin"
cat >"$repo/bin/gh" <<'EOF'
#!/bin/sh
[ "$1 $2" = "pr view" ] || exit 1
case "$*" in
  *autoMergeRequest*) echo "${AUTOMERGE_OFF:-true}" ;;
  *) echo 4242 ;;
esac
EOF
chmod +x "$repo/bin/gh"
PATH="$repo/bin:$PATH"
failed=0

check() {
  want=$1
  cmd=$2
  out=$(jq -n --arg c "$cmd" --arg d "${3:-$repo}" '{tool_input: {command: $c}, cwd: $d}' | sh "$hook")
  got=pass
  case "$out" in *'"ask"'*) got=ask ;; *'"deny"'*) got=deny ;; esac
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd"
    failed=1
  fi
}

# Its own pull request, without asking. The e2e-ready label goes through only on the pull
# request of the current branch.
check pass "gh pr edit 4242 --add-label e2e-ready"
check ask "gh pr edit 4243 --add-label e2e-ready"
# Disabling auto-merge on it lowers the risk; enabling it or merging still asks.
check pass "gh pr merge 4242 --disable-auto"
check ask "gh pr merge 4243 --disable-auto"
check ask "gh pr merge 4242 --auto --rebase"
check ask "gh pr merge 4242 --disable-auto --rebase"
check ask "gh pr merge 4242 --disable-auto && git push"
# A CodeRabbit review request too, with exactly that body.
check pass "gh pr comment 4242 --body \"@coderabbitai review\""
check pass "gh pr comment 4242 --body '@coderabbitai full review'"
check pass "gh pr comment 4242 -b \"@coderabbitai full review\""
check ask "gh pr comment 4243 --body \"@coderabbitai review\""
check ask "gh pr comment 4242 --body \"@coderabbitai review please\""
check ask "gh pr comment 4242 --body \"@coderabbitai resolve\""
check ask "gh pr comment 4242 --body-file x.md"
check ask "gh pr comment 4242 --body \"@coderabbitai review\" && git push"
check ask "gh pr comment 4242 --body \"@coderabbitai review\" --repo other/repo"
check pass "git push origin $branch"
check pass "git push -u origin $branch"
check pass "git push --force-with-lease=$branch:0123abc origin $branch"
# With auto-merge enabled on its pull request, the push is refused (disable it first); when gh
# cannot tell, the push asks.
export AUTOMERGE_OFF
AUTOMERGE_OFF=false
check deny "git push origin $branch"
check deny "git push --force-with-lease origin $branch"
AUTOMERGE_OFF="error: could not connect"
check ask "git push origin $branch"
AUTOMERGE_OFF="no pull requests found for branch \"$branch\""
check pass "git push -u origin $branch"
AUTOMERGE_OFF=true
check pass "gh api graphql -f query='query { viewer { login } }' --jq '.data | .viewer'"
check pass "gh api graphql -f query='query { viewer { login } }'"
# Double quotes are literal inside the single-quoted query: the open threads query of the
# pull-request skill passes.
check pass "gh api graphql -f query='query { repository(owner: \"enix\", name: \"kube-image-keeper\") { pullRequest(number: 12) { reviewThreads(first: 100) { nodes { id isResolved path } } } } }' --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved | not)'"
check ask "gh api graphql -f query='query { search(query: \"mutation\") { issueCount } }'"
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
# The acceptance tasks go through the program's guard; only cleanup, which ends a run, asks. The
# writing ones pass only with test/acceptance and the Taskfile as on origin/main.
check ask "task acceptance-setup -- --kubeconfig k --context c"
echo /bin/ >>"$repo/.git/info/exclude"
mkdir -p "$repo/test/acceptance"
echo guard >"$repo/test/acceptance/guard.go"
echo tasks >"$repo/Taskfile.yaml"
git -C "$repo" add test Taskfile.yaml
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q -m main
git -C "$repo" update-ref refs/remotes/origin/main HEAD
check pass "task acceptance-setup -- --kubeconfig k --context c"
check pass "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
check pass "task acceptance-delete -- --kubeconfig k --context c pod/p"
check ask "cd /elsewhere && task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
check ask "task acceptance-apply -- --kubeconfig k --context c --file \$(echo f.yaml)"
# A single-quoted argument is literal: a regex passes, anything that leaves the quotes asks.
check pass "task acceptance-inspect -- --kubeconfig k --context c --grep 'Denied|Failed' --metrics 'kuik_(a|b)\$'"
check pass "task acceptance-inspect -- --kubeconfig k --context c --grep='a|b'"
check ask "task acceptance-inspect -- --kubeconfig k --context c --grep 'a' ; rm x"
check ask "task acceptance-inspect -- --kubeconfig k --context c --grep 'a'|sh"
check ask "task acceptance-inspect -- --kubeconfig k --context c --grep 'a' 2>&1 | grep -v x | cut -c1-9"
check ask "task acceptance-inspect -- --kubeconfig k --context c --grep \"\$(id)\""
echo changed >"$repo/test/acceptance/guard.go"
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
git -C "$repo" checkout -q -- test/acceptance/guard.go
echo new >"$repo/test/acceptance/new.go"
check ask "task acceptance-delete -- --kubeconfig k --context c pod/p"
rm "$repo/test/acceptance/new.go"
echo changed >"$repo/Taskfile.yaml"
check ask "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" checkout -q -- Taskfile.yaml
check pass "task acceptance-setup -- --kubeconfig k --context c"
# A fixture goes through the guard of origin/main, Markdown never runs: a branch of new scenarios
# passes. Anything beside them still asks.
mkdir -p "$repo/test/acceptance/fixtures/sub"
echo fixture >"$repo/test/acceptance/fixtures/12-new.yaml"
echo scenarios >"$repo/test/acceptance/scenarios.md"
check pass "task acceptance-setup -- --kubeconfig k --context c"
echo fixture >"$repo/test/acceptance/fixtures/sub/13-nested.yaml"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm -r "$repo/test/acceptance/fixtures/sub"
echo new >"$repo/test/acceptance/new.go"
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
# Changed code passes once approved as it is, and asks again as soon as it moves. Approving always
# asks, and so does any command naming the approval.
fingerprint="$(dirname "$hook")/acceptance-fingerprint.sh"
(cd "$repo" && sh "$fingerprint") >"$repo/.git/kuik-acceptance-approved"
check pass "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
echo moved >"$repo/test/acceptance/new.go"
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
check ask "task acceptance-approve"
# Its question lists the changes only for the plain command run from the top of the worktree:
# from a subdirectory or behind a cd, it says it cannot list them.
reason() {
  jq -n --arg c "$2" --arg d "$1" '{tool_input: {command: $c}, cwd: $d}' | sh "$hook" |
    jq -r '.hookSpecificOutput.permissionDecisionReason // ""'
}
case "$(reason "$repo" "task acceptance-approve")" in *'Changed files:'*'new.go'*) ;; *)
  echo "FAIL: the approval question lists the changes: task acceptance-approve"
  failed=1
  ;;
esac
for c in "cd test && task acceptance-approve" "task acceptance-approve -- x"; do
  case "$(reason "$repo" "$c")" in *'cannot list'*) ;; *)
    echo "FAIL: the approval question says it cannot list the changes: $c"
    failed=1
    ;;
  esac
done
case "$(reason "$repo/test" "task acceptance-approve")" in *'cannot list'*) ;; *)
  echo "FAIL: the approval question says it cannot list the changes from a subdirectory"
  failed=1
  ;;
esac
check ask "cp x .git/kuik-acceptance-approved"
check ask "cp x \".git/kuik-acceptance-approve\"d"
# The approval covers tracked changes too, not the fixtures or Markdown, and only for this
# origin/main. An empty approval never matches.
(cd "$repo" && sh "$fingerprint") >"$repo/.git/kuik-acceptance-approved"
echo fixture2 >"$repo/test/acceptance/fixtures/12-new.yaml"
check pass "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
echo changed >"$repo/test/acceptance/guard.go"
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
(cd "$repo" && sh "$fingerprint") >"$repo/.git/kuik-acceptance-approved"
check pass "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q --allow-empty -m moved
git -C "$repo" update-ref refs/remotes/origin/main HEAD
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
: >"$repo/.git/kuik-acceptance-approved"
check ask "task acceptance-apply -- --kubeconfig k --context c --file f.yaml"
rm "$repo/.git/kuik-acceptance-approved"
git -C "$repo" checkout -q -- test/acceptance/guard.go
rm "$repo/test/acceptance/new.go" "$repo/test/acceptance/fixtures/12-new.yaml" "$repo/test/acceptance/scenarios.md"
check pass "task acceptance-setup -- --kubeconfig k --context c"
# Git must compare against the real origin/main and see the tree as Go compiles it: a local
# branch named origin/main, a replace object, a file hidden from the index, a local attributes
# file, a deletion folded into a rename to Markdown, all ask. So does a task run from a
# subdirectory, which picks its own Taskfile.
mkdir -p "$repo/sub"
check ask "task acceptance-setup -- --kubeconfig k --context c" "$repo/sub"
rmdir "$repo/sub"
check ask "git fetch . HEAD:remotes/origin/main"
check ask "git symbolic-ref refs/remotes/origin/main refs/heads/x"
check ask "git remote set-url origin git@example.com:fork.git"
echo changed >"$repo/test/acceptance/guard.go"
git -C "$repo" branch origin/main HEAD
check ask "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" branch -q -D origin/main
git -C "$repo" checkout -q -- test/acceptance/guard.go
git -C "$repo" replace "$(git -C "$repo" rev-parse HEAD)" "$(git -C "$repo" rev-parse HEAD~1)"
check ask "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" replace -d "$(git -C "$repo" rev-parse HEAD)" >/dev/null
git -C "$repo" update-index --assume-unchanged test/acceptance/guard.go
check ask "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" update-index --no-assume-unchanged test/acceptance/guard.go
echo '*.go filter=x' >"$repo/.git/info/attributes"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm "$repo/.git/info/attributes"
git -C "$repo" mv test/acceptance/guard.go test/acceptance/guard.md
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q -m rename
check ask "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" reset -q --hard HEAD~1
check pass "task acceptance-setup -- --kubeconfig k --context c"
# Anything else in the working copy can change what runs: an untracked go.work, an ignored file Go
# would compile, another Taskfile name.
echo 'go 1.26' >"$repo/go.work"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm "$repo/go.work"
echo /test/acceptance/hidden.go >>"$repo/.git/info/exclude"
echo hidden >"$repo/test/acceptance/hidden.go"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm "$repo/test/acceptance/hidden.go"
mkdir -p "$repo/api/kuik"
echo /api/kuik/hidden.go >>"$repo/.git/info/exclude"
echo hidden >"$repo/api/kuik/hidden.go"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm "$repo/api/kuik/hidden.go"
echo tasks >"$repo/Taskfile.yml"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm "$repo/Taskfile.yml"
# A worktree nested under .claude/worktrees/ is not read by go or task from the top: it passes.
# A plain ignored directory there with Go files in it still asks.
echo /.claude/worktrees/ >>"$repo/.git/info/exclude"
git -C "$repo" worktree add -q --detach "$repo/.claude/worktrees/topic"
check pass "task acceptance-setup -- --kubeconfig k --context c"
git -C "$repo" worktree remove --force "$repo/.claude/worktrees/topic"
mkdir -p "$repo/.claude/worktrees/plain"
echo hidden >"$repo/.claude/worktrees/plain/hidden.go"
check ask "task acceptance-setup -- --kubeconfig k --context c"
rm -r "$repo/.claude/worktrees/plain"
# Another Taskfile picked through a cluster, a quote, a backslash or a newline.
check ask "task acceptance-setup -tevil.yml -- --kubeconfig k --context c"
check ask "task acceptance-setup -\\t evil.yml"
check ask "task acceptance-setup -'t' evil.yml"
check ask "task acceptance-setup \"a -- b\" -t evil.yml"
check ask "$(printf 'task acceptance-setup -- --kubeconfig k\necho x')"
check pass "task acceptance-setup -- --kubeconfig k --context c"
check ask "task acceptance-cleanup -- --kubeconfig k --context c"
# Moving origin/main by hand would make changed code pass for reviewed code.
check ask "git update-ref refs/remotes/origin/main HEAD"
check ask "git -C . update-ref refs/remotes/origin/main HEAD"
check ask "git fetch . HEAD:refs/remotes/origin/main"
check pass "git fetch origin"
# Another Taskfile or directory is not the task the allow rules name.
check ask "task acceptance-check --taskfile /tmp/x.yml -- --kubeconfig k --context c"
check ask "task -d /tmp acceptance-check"
check ask "task -t /tmp/x.yml lint"
check ask "task -g lint"
check pass "task test -- -timeout 5m"
# Options and quotes of another command on the line are not task's.
check pass "task lint-fix 2>&1 | grep -E '^test|issues'; go vet -tags e2e ./test/e2e/ && echo vet-ok"
check pass "task test-short && go test -tags e2e ./test/e2e/"
check ask "go vet ./... && task -d /tmp lint"
check ask "echo ok; task 'lint'"
check ask "cd x && task -t y.yml lint"
check ask "FOO=1 task -t y.yml lint"
check ask "echo ok; A=1 B=2 task -d /tmp lint"
check pass "git commit -m 'fix: a plain task lint-fix call' -m \"go vet -tags e2e\""
# A wrapper does not hide a task call. Only the -m of a git commit, tag, notes or merge is a
# message: elsewhere, the words after -m stay visible.
check ask "timeout 600 task test -t /tmp/x.yml"
check ask "time task lint -d /tmp"
# The -- of another command does not hide a later task call.
check ask "git log -- x; task lint -t /tmp/x.yml"
check ask "bash -c 'task -t /tmp/evil.yml lint'"
check ask "sh -c \"task lint -d /tmp\""
check pass "task test -- -run X; task lint"
# --output writes anywhere, a trusted ref included.
check ask "git log -1 --format=%H --output=.git/refs/remotes/origin/main"
check ask "git diff --output=/tmp/x.patch"
check ask "cp HEAD .git/packed-refs"
check pass "git log --oneline -3"
check ask "mytool -m 'x' ; git push origin main"
check ask "echo -m x && git push origin main"
check pass "git -C ../wt commit -m \"fix(helm): let helm upgrade keep it\""
check pass "git tag -a v1 -m 'git push origin main is manual'"
check ask "go run ./test/acceptance check --kubeconfig k --context c"
check ask "go build -o bin/acceptance ./test/acceptance"
check pass "task acceptance-check -- --kubeconfig k --context c"
check pass "task acceptance-inspect -- --kubeconfig k --context c"
check pass "task acceptance-validate"
# The read-only tasks too run the working copy's code: changed, it could write.
echo changed >"$repo/test/acceptance/guard.go"
check ask "task acceptance-inspect -- --kubeconfig k --context c"
check ask "task acceptance-validate"
git -C "$repo" checkout -q -- test/acceptance/guard.go
check pass "go test -tags=e2e ./test/e2e/"
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
# A commit message names commands without running them.
check pass "git commit -m \"feat(helm): add podLabels\""
check pass "git commit -m \"fix(helm): let helm upgrade keep the policy\""
check pass "git commit -m 'docs: say when to git push and gh pr create' -m 'kubectl apply is not run here'"
check pass "git commit --message='fix: helm install keeps it'"
check ask "git commit -m \"\$(helm upgrade x y)\""
check ask "git commit -m \"fix: x\" && git push origin main"
check ask "git commit -m 'x'; helm upgrade x y"
# A command in quotes runs too: bash -c, sh -c, eval.
check ask "bash -c \"git push origin main\""
check ask "bash -c -m \"git push origin main\""
check ask "sh -c 'helm upgrade x y'"
check ask "eval 'gh pr merge 12'"
check ask "git commit -m 'x' && bash -c -m \"git push origin main\""
# A -m inside quotes is text: it cannot open a fake message that hides what follows.
check ask "echo \"-m '\" ; git push origin main ; echo \"'\""
check ask "git commit --author \"-m '\" ; git push origin main ; echo \"'\""
check ask "git commit -m \"fix -m '\" && git push origin main && echo \"'\""
check ask "git commit -m \"\`git push origin main\`\""
check pass "git commit -m \"fix: the \\\"git push\\\" step\""
check pass "git commit --message='fix: kubectl apply is not run here'"
check pass "gh pr view 713"
# A gh filter or template naming env could print the environment and its tokens.
check ask "gh pr view 713 --json number --jq 'env.GH_TOKEN'"
check ask "gh pr view 713 --json number -q '\$ENV'"
check ask "gh pr view 713 --json number --template '{{ env }}'"
check ask "gh api graphql -f query='query { viewer { login } }' --jq 'env'"
check pass "gh pr view 713 --json environment"
check ask "gh pr view 713 --json number --jq e'n'v.GH_TOKEN"
check ask "gh pr view 713 --json number -q '\$''ENV'"
check ask "gh pr view 713 --json number --jq e\\nv"
check pass "go env GOPATH"

[ "$failed" -eq 0 ] && echo "all cases pass"
exit "$failed"
