#!/usr/bin/env sh
# PreToolUse hook (Bash): commands that leave the working copy (push, pull requests,
# releases, deployments) ask the user first, whatever the permission mode. They are not
# forbidden: notes/0003 says the agent never does them on its own. A session drives its own
# pull request without asking for 3 single commands: pushing its feature branch, adding the
# e2e-ready label, and a GraphQL read. Merging still asks.
set -eu

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cmd" ] || exit 0

# allowed holds for a single command a session may run on its own pull request. Each form
# must match the whole command, start to end, with characters the shell gives no meaning
# to: no quote stripping, so no quoting trick can hide a second command, a substitution
# or a redirection. Anything else falls through to the check below.
allowed() {
  case "$cmd" in
    *'
'* | *"$(printf '\r')"*) return 1 ;;
  esac
  # Its own feature branch, named, to the remote branch of the same name: never main, a
  # maintenance or release branch, never a plain --force, a tag or a refspec. A bare
  # `git push` or `git push origin` goes where push.default and the upstream say, which may
  # be main: it asks. The name must start with a letter or a digit, or a branch named
  # --mirror would turn the push into an option. origin must be this repository on GitHub,
  # with no push refmap, push URL or URL rewrite that could send the push elsewhere.
  branch=$(git -C "${cwd:-.}" symbolic-ref --quiet --short HEAD 2>/dev/null || true)
  config=$(git -C "${cwd:-.}" config --get-regexp '^(remote\.origin\.push|url\..*\.(push)?insteadof)$' 2>/dev/null || true)
  [ -z "$config" ] || branch=
  # push.followTags would push the reachable annotated tags along with the branch.
  [ "$(git -C "${cwd:-.}" config --get --bool push.followTags 2>/dev/null || true)" != true ] || branch=
  for key in remote.origin.url remote.origin.pushurl; do
    url=$(git -C "${cwd:-.}" config --get-all "$key" 2>/dev/null || true)
    case "$url" in
      '') [ "$key" = remote.origin.pushurl ] || branch= ;;
      git@github.com:enix/kube-image-keeper.git | https://github.com/enix/kube-image-keeper.git | https://github.com/enix/kube-image-keeper) ;;
      *) branch= ;;
    esac
  done
  case "$branch" in
    '' | main | master | release* | *.x | v[0-9]*) ;;
    *)
      if printf '%s' "$branch" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._/-]*$'; then
        b=$(printf '%s' "$branch" | sed 's/\./\\./g')
        flags='([[:space:]]+(-u|--set-upstream|--force-with-lease(=[A-Za-z0-9._/:-]+)?))*'
        printf '%s' "$cmd" | grep -Eq "^git[[:space:]]+push${flags}[[:space:]]+origin[[:space:]]+${b}${flags}[[:space:]]*\$" && return 0
      fi
      ;;
  esac
  # The e2e-ready label, on the pull request of the current branch only.
  if printf '%s' "$cmd" | grep -Eq '^gh[[:space:]]+pr[[:space:]]+edit[[:space:]]+[0-9]+[[:space:]]+--add-label[[:space:]]+e2e-ready[[:space:]]*$'; then
    n=$(printf '%s' "$cmd" | sed 's/^gh[[:space:]]*pr[[:space:]]*edit[[:space:]]*\([0-9]*\).*/\1/')
    own=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json number --jq .number 2>/dev/null || true)
    [ -n "$own" ] && [ "$n" = "$own" ] && return 0
  fi
  # A GraphQL read: one query in single quotes, with no quote, backslash, $ or backtick
  # inside, so the shell passes it as written and "mutation" cannot be split or built;
  # optionally a --jq filter written the same way.
  sq="[^'\\\$\`]*"
  graphql="^gh[[:space:]]+api[[:space:]]+graphql[[:space:]]+-f[[:space:]]+query='${sq}'([[:space:]]+--jq[[:space:]]+'${sq}')?[[:space:]]*\$"
  if printf '%s' "$cmd" | grep -Eq "$graphql" && ! printf '%s' "$cmd" | grep -iq 'mutation'; then
    return 0
  fi
  return 1
}
allowed && exit 0

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
# the e2e tasks create and delete a Kind cluster; smoke-run and smoke-cleanup write to the
# cluster they are given (smoke-check only reads).
task_outbound="(task|make)[[:space:]]+${opts}(deploy|kind-deploy|undeploy|install|uninstall|docker-push|docker-buildx|run(:[a-z-]+)?|setup-test-e2e|test-e2e|cleanup-test-e2e|smoke-run|smoke-cleanup)"
# The smoke suite run directly, around the tasks: any test of it may write to a cluster.
go_smoke="go[[:space:]]+test[[:space:]][^;&|]*-tags[[:space:]=]+[^[:space:];&|]*smoke"
docker_push="docker[[:space:]]+${opts}((image|manifest)[[:space:]]+)?push|docker[[:space:]][^;&|]*--push(=[^[:space:]]*)?"
helm_outbound="helm[[:space:]]+${opts}(install|upgrade|uninstall|delete|rollback)"
kubectl_outbound="kubectl[[:space:]]+${opts}(apply|create|delete|replace|patch|edit|scale|rollout|drain|cordon|uncordon|taint|label|annotate|set|expose|autoscale|exec|cp|run|debug|attach)"
pattern="(^|[;&|(\`[:space:]])($git_push|$gh_outbound|$gh_api|$task_outbound|$go_smoke|$docker_push|$helm_outbound|$kubectl_outbound)([[:space:]]|\)|$)"

if printf '%s' "$cmd" | grep -Eq "$pattern"; then
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: "This command leaves the working copy (push, pull request, image, release, deployment or cluster change): the user confirms it explicitly, see notes/0003."
    }
  }'
fi
