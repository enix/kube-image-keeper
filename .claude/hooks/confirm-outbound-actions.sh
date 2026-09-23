#!/usr/bin/env sh
# PreToolUse hook (Bash): commands that leave the working copy (push, pull requests,
# releases, deployments) always ask the user first, whatever the permission mode. They
# are not forbidden: notes/0003 says the agent never does them on its own.
set -eu

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
cmd=$(jq -r '.tool_input.command // empty')
[ -n "$cmd" ] || exit 0

# "opts" lets global options, or for task and make other task names, sit between the
# executable and its subcommand (helm --namespace ns install, kubectl -n ns apply,
# gh -R repo pr create, task build deploy). For git, the subcommand is the first word that
# is not an option, nor the value of one of the few options that take it as a separate word:
# any word would let `git stash push` or `git log --grep push` through.
opts='([^;&|]*[[:space:]])?'
git_opts='((-C|-c|--git-dir|--work-tree|--namespace|--config-env)[[:space:]]+[^[:space:]]+[[:space:]]+|-[^[:space:]]*[[:space:]]+)*'
git_push="git[[:space:]]+${git_opts}push"
gh_outbound="gh[[:space:]]+${opts}(pr[[:space:]]+(create|merge|close|reopen|ready|edit|comment|review)|issue[[:space:]]+(create|comment|close|reopen|edit|delete|transfer|lock)|release|repo[[:space:]]+(create|delete|edit)|workflow[[:space:]]+run|run[[:space:]]+(rerun|cancel))"
# gh api writes with an explicit method, or implicitly (POST) as soon as it sends fields.
gh_api="gh[[:space:]]+${opts}api[[:space:]][^;&|]*(-X|--method)[[:space:]=]*(POST|PATCH|PUT|DELETE)|gh[[:space:]]+${opts}api[[:space:]][^;&|]*(-f|-F|--field|--raw-field|--input)"
# The Makefile forwards to task. run and its run:* subtasks use the current kubeconfig;
# the e2e tasks create and delete a Kind cluster.
task_outbound="(task|make)[[:space:]]+${opts}(deploy|kind-deploy|undeploy|install|uninstall|docker-push|docker-buildx|run(:[a-z-]+)?|setup-test-e2e|test-e2e|cleanup-test-e2e)"
docker_push="docker[[:space:]]+${opts}((image|manifest)[[:space:]]+)?push|docker[[:space:]][^;&|]*--push(=[^[:space:]]*)?"
helm_outbound="helm[[:space:]]+${opts}(install|upgrade|uninstall|delete|rollback)"
kubectl_outbound="kubectl[[:space:]]+${opts}(apply|create|delete|replace|patch|edit|scale|rollout|drain|cordon|uncordon|taint|label|annotate|set|expose|autoscale|exec|cp|run|debug|attach)"
pattern="(^|[;&|(\`[:space:]])($git_push|$gh_outbound|$gh_api|$task_outbound|$docker_push|$helm_outbound|$kubectl_outbound)([[:space:]]|\)|$)"

if printf '%s' "$cmd" | grep -Eq "$pattern"; then
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: "This command leaves the working copy (push, pull request, image, release, deployment or cluster change): the user confirms it explicitly, see notes/0003."
    }
  }'
fi
