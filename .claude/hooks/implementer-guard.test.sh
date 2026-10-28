#!/usr/bin/env sh
# shellcheck disable=SC2016 # the commands under check are literal: nothing may expand
# Checks implementer-guard.sh on a table of commands: "refuse" when the implementer agent must
# hand the command back, "pass" when it may run it. The hook only reads them: task lint-hooks.
set -eu

hook="$(cd "$(dirname "$0")" && pwd)/implementer-guard.sh"
failed=0

check() {
  want=$1
  cmd=$2
  got=pass
  jq -n --arg c "$cmd" '{tool_input: {command: $c}}' | sh "$hook" 2>/dev/null || got=refuse
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd"
    failed=1
  fi
}

# History rewrites, pushes and branch moves go back to the owning session.
check refuse "git push origin topic"
check refuse "git -C ../wt rebase main"
check refuse "git reset --hard HEAD~1"
check refuse "git commit --amend --no-edit"
check refuse "git stash"
check refuse "git checkout main"
check refuse "git branch -D topic"
check refuse "go test ./... && git push"
check refuse "FOO=1 git push origin topic"
check refuse "git commit -m \"\$(git push)\""
# GitHub, clusters and the e2e tasks too (reading an issue or a pull request aside, below).
check refuse "gh pr list"
check refuse "kubectl get pods"
check refuse "kind create cluster"
check refuse "docker build ."
check refuse "helm install kuik ./helm/kube-image-keeper"
check refuse "helm upgrade kuik ./helm/kube-image-keeper"
check refuse "task test-e2e"
check refuse "task deploy IMG=x"
check refuse "echo ok; helm uninstall kuik"
# Local work passes, whatever words a path, a message or an argument holds.
check pass "git add helm/kube-image-keeper/templates/policy.yaml"
check pass "git commit -m \"feat(helm): add the admission policy\""
check pass "git commit -m 'fix: reset the counter' -m 'kubectl apply and git push stay manual'"
check pass "git log --grep reset --oneline"
check pass "git diff -- internal/registry/checkout.go"
check pass "git status --short"
check pass "git branch --show-current"
check pass "helm template kuik ./helm/kube-image-keeper --show-only templates/policy.yaml"
check pass "helm lint ./helm/kube-image-keeper"
check pass "task lint-fix"
check pass "task test-short"
check pass "go test ./internal/... -run Test"
check pass "grep -rn 'kubectl' docs/"
# A tool named as a bare argument looks like a command: quote it to search for it.
check refuse "grep -rn kubectl docs/"
# Wrappers, keywords, subshells and paths do not hide a command.
check refuse "(git push origin topic)"
check refuse "{ git push; }"
check refuse "if true; then git push; fi"
check refuse "env FOO=1 git push"
check refuse "timeout 60 git push origin topic"
check refuse "xargs git push < refs"
check refuse "/usr/bin/git push"
check refuse "! git reset --hard"
check refuse "bash -c \"git stash\""
check refuse "\"gh\" pr view 1"
check refuse "\`kubectl\` get pods"
check refuse "git --git-dir .git push"
check refuse "git send-pack origin topic"
check refuse "git branch --delete topic"
check refuse "git branch --force topic HEAD~1"
check refuse "podman push img"
check refuse "make deploy"
check refuse "task acceptance-approve"
# It reads the issue and the pull request it works on; every other gh command stays refused.
check pass "gh issue view 748 --json body"
check pass "gh pr view 802 --json files"
check pass "gh pr view 802 --repo enix/kube-image-keeper --json body --jq '.body'"
check refuse "gh issue comment 748 --body x"
check refuse "gh pr view 802 | head"
check refuse "gh api repos/enix/kube-image-keeper/issues"
check refuse "gh pr view 802 --repo other/repo"
check refuse "gh pr view 802 --json body --jq env.GH_TOKEN"
check refuse "gh pr view 802; git push"
# acceptance-validate decodes the fixtures only: it passes alone, never with another command.
check pass "task acceptance-validate"
check pass "task acceptance-validate -- --kuik-namespace kuik-system"
check refuse "task acceptance-validate deploy"
check refuse "task acceptance-validate --taskfile /tmp/x.yml"
check refuse "task acceptance-validate && git push"
check refuse "task acceptance-validate; task acceptance-apply -- --file x"
check refuse "task acceptance-validate \$(git push)"
check refuse "task acceptance-validate
git push origin topic"
check refuse "task acceptance-apply -- --file x"
check refuse "go run ./test/acceptance check"
check refuse "go run ./cmd reconciler"
check refuse "go run ./cmd/main.go reconciler"
check refuse "go run ./test/acceptance/ check"
check refuse "go run ./test/acceptance/main.go check"
check refuse "go -C ./test/acceptance run . apply"
check refuse "go -C cmd run . reconciler"
# A -m inside quotes cannot open a fake message that hides a command.
check refuse "echo \"-m '\" ; git push ; echo \"'\""
# Paths, flags and messages that name a tool still pass.
check pass "go run ./hack/testoutline -diff HEAD^1"
check pass "git add helm/kube-image-keeper/values.yaml"
check pass "cat .github/workflows/e2e.yaml"

[ "$failed" -eq 0 ] && echo "all cases pass"
exit "$failed"
