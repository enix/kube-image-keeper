#!/usr/bin/env sh
# Checks the Bash ask rules of settings.json on a table of commands, reading each rule as the
# glob Claude Code matches it with (* spans any characters): "ask" when a rule matches, "pass"
# when none does. The task rules must match a task name, never a path that holds it (a worktree
# named rewrite-docker-library). Run by task lint-hooks.
set -eu

settings="$(cd "$(dirname "$0")" && pwd)/settings.json"
rules=$(jq -r '.permissions.ask[] | select(startswith("Bash(")) | sub("^Bash\\("; "") | sub("\\)$"; "")' "$settings")
failed=0

check() {
  want=$1
  cmd=$2
  got=pass
  while IFS= read -r rule; do
    # shellcheck disable=SC2254 # the rule is a glob on purpose
    case "$cmd" in $rule) got=ask ;; esac
  done <<EOF
$rules
EOF
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $cmd"
    failed=1
  fi
}

wt=/home/dev/kuik/.claude/worktrees/rewrite-docker-library
check pass "task -d $wt lint-fix"
check pass "task -d $wt setup-envtest"
check pass "task -d $wt --list"
check pass "task -d /home/dev/kuik/.claude/worktrees/install-guide lint"
check pass "task -d /home/dev/kuik/.claude/worktrees/runner-fix lint"
check pass "task test -- -run TestX"
check pass "task build-installer"
check ask "task docker-build IMG=x"
check ask "task -d $wt docker-push IMG=x"
check ask "task deploy IMG=x"
check ask "task build deploy"
check ask "task -d $wt kind-deploy"
check ask "task install"
check ask "task uninstall"
check ask "task undeploy"
check ask "task test-e2e"
check ask "task setup-test-e2e"
check ask "task cleanup-test-e2e"
check ask "task run"
check ask "task run:webhook"
check ask "task acceptance-cleanup -- --kubeconfig k"
check ask "task acceptance-config -- --kubeconfig k"

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
