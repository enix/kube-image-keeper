#!/usr/bin/env sh
# PreToolUse hook (Bash) of the implementer agent: it refuses what belongs to the session that
# owns the pull request (push, history rewrites, GitHub, clusters, e2e). It refuses rather than
# asks: the agent stops and hands the step back. Only commands count, not the words a commit
# message, a path or an argument holds (feat(helm), helm/kube-image-keeper, fix: reset x).
set -eu

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
cmd=$(jq -r '.tool_input.command // empty')
[ -n "$cmd" ] || exit 0

# A commit message names commands without running them: its quoted value is masked,
# following the shell's quoting (mask-messages.sh).
. "$(dirname "$0")/mask-messages.sh"
cmd=$(mask_messages "$cmd")

# A command word is not preceded by a letter, a digit or _ . -, so it is caught after ; & |
# ( { ! $( a backtick, a quote, a keyword (then, do), a wrapper (env, timeout, xargs...) or
# a VAR=value word, and as an absolute or relative path (/usr/bin/git). It must end at a space,
# an operator (; & | ) }) or the end: helm/kube-image-keeper and feat(helm) are not the helm
# command.
start="(^|[^[:alnum:]_.-])([^[:space:];&|()]*/)?"
end="([[:space:];&|)}]|\$)"
# The words of one command: up to the next ; & | or ).
args="([^;&|)]*[[:space:]])?"
# git's subcommand is its first word that is not an option nor the value of an option that
# takes one, so git log --grep reset is not a reset.
git="git[[:space:]]+((-C|-c|--git-dir|--work-tree|--namespace|--exec-path|--config-env)[[:space:]]+[^[:space:]]+[[:space:]]+|-[^[:space:]]+[[:space:]]+)*"
git_rewrite="${git}(push|send-pack|subtree|rebase|reset|stash|checkout|switch|restore|clean|worktree|filter-branch|filter-repo|update-ref|symbolic-ref|replace|branch[[:space:]]+${args}(-[dDmMfC]|--delete|--force|--move|--copy))"
git_amend="${git}commit[^;&|)]*--amend"
tools="(gh|kubectl|kind|docker|podman|skopeo|crane|oras)"
# helm renders and lints locally; only what reaches a cluster or a registry is refused.
helm_write="helm[[:space:]]+${args}(install|upgrade|uninstall|delete|rollback|test|push|registry|plugin)"
# task and make (the Makefile forwards to task): the cluster, e2e, image and acceptance tasks.
task_outbound="(task|make)[[:space:]]+${args}(deploy|kind-deploy|undeploy|install|uninstall|run|test-e2e|setup-test-e2e|cleanup-test-e2e|docker-push|docker-buildx|acceptance-[a-z-]+)"
# go run of the manager or of the acceptance program reaches a cluster.
# go run of the manager or of the acceptance program, wherever the path sits: after run, or
# before it as the value of -C (go -C ./test/acceptance run . apply).
go_run="go[[:space:]][^;&|)]*(run[[:space:]][^;&|)]*(cmd|test/acceptance)|(cmd|test/acceptance)[^;&|)]*[[:space:]]run[[:space:]])[^;&|)]*"
# A command with its subcommand may end at a quote (bash -c "git stash"); a tool name alone may
# not, or grep 'kubectl' docs/ would be refused.
pattern="${start}((${git_rewrite}|${git_amend}|${helm_write}|${task_outbound}|${go_run})([[:space:];&|)}\"'\`]|\$)|${tools}${end})"
# A tool name alone in quotes is a command when it opens one ("gh" pr view, `kubectl` get), an
# argument otherwise (grep 'kubectl' docs/).
quoted="(^|[;&|({!]|\\\$\\()[[:space:]]*[\"'\`]([^[:space:];&|()]*/)?(git|gh|kubectl|kind|docker|podman|skopeo|crane|oras|helm|task|make|go)[\"'\`]"

# acceptance-validate decodes the fixtures and reaches no cluster: it passes alone, on one line,
# with plain arguments after `--` only, so nothing can ride along with it: no second task, no
# task option (grep matches line by line).
case "$cmd" in
  *'
'* | *"$(printf '\r')"*) ;;
  *)
    if printf '%s' "$cmd" | grep -Eq '^task[[:space:]]+acceptance-validate([[:space:]]+--([[:space:]]+[A-Za-z0-9=,./:_@+-]+)*)?[[:space:]]*$'; then
      exit 0
    fi
    ;;
esac

if printf '%s' "$cmd" | grep -Eq "$pattern" || printf '%s' "$cmd" | grep -Eq "$quoted"; then
  echo "implementer: this command is reserved to the session that owns the pull request (push, history rewrite, GitHub, cluster, e2e). Stop and hand it back; never swap in another command, task or wrapper to get around this." >&2
  exit 2
fi
