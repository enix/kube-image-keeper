#!/usr/bin/env sh
# PreToolUse hook (Bash): commands that leave the working copy (push, pull requests,
# releases, deployments) ask the user first, whatever the permission mode. They are not
# forbidden: notes/0003 says the agent never does them on its own. A session drives its own
# pull request without asking for 5 single commands: pushing its feature branch, adding the
# e2e-ready label, disabling auto-merge, asking CodeRabbit for a review, and a GraphQL read.
# Merging, and enabling auto-merge, still ask.
set -eu

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cmd" ] || exit 0

# automerge_off holds when the pull request of the current branch has no auto-merge enabled,
# or when the branch has no pull request yet. With auto-merge enabled, GitHub would merge a
# push as soon as the checks pass, before anyone reviews it: the push asks. Any other answer
# of gh (network, auth) asks too.
automerge_off() {
  out=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json autoMergeRequest --jq '.autoMergeRequest == null' 2>&1) && [ "$out" = true ] && return 0
  case "$out" in *'no pull requests found'*) return 0 ;; esac
  # Known to be enabled: refuse, and say how to go on; any other answer asks below.
  if [ "$out" = false ]; then
    n=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json number --jq .number 2>/dev/null || true)
    jq -n --arg n "${n:-<n>}" '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: ("Auto-merge is enabled on this pull request: GitHub would merge this push before anyone reviews it. Run `gh pr merge " + $n + " --disable-auto` first, alone, then push again.")
      }
    }'
    exit 0
  fi
  return 1
}

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
        printf '%s' "$cmd" | grep -Eq "^git[[:space:]]+push${flags}[[:space:]]+origin[[:space:]]+${b}${flags}[[:space:]]*\$" && automerge_off && return 0
      fi
      ;;
  esac
  # The e2e-ready label, on the pull request of the current branch only.
  if printf '%s' "$cmd" | grep -Eq '^gh[[:space:]]+pr[[:space:]]+edit[[:space:]]+[0-9]+[[:space:]]+--add-label[[:space:]]+e2e-ready[[:space:]]*$'; then
    n=$(printf '%s' "$cmd" | sed 's/^gh[[:space:]]*pr[[:space:]]*edit[[:space:]]*\([0-9]*\).*/\1/')
    own=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json number --jq .number 2>/dev/null || true)
    [ -n "$own" ] && [ "$n" = "$own" ] && return 0
  fi
  # Disabling auto-merge on the pull request of the current branch: it only lowers the risk.
  if printf '%s' "$cmd" | grep -Eq '^gh[[:space:]]+pr[[:space:]]+merge[[:space:]]+[0-9]+[[:space:]]+--disable-auto[[:space:]]*$'; then
    n=$(printf '%s' "$cmd" | sed 's/^gh[[:space:]]*pr[[:space:]]*merge[[:space:]]*\([0-9]*\).*/\1/')
    own=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json number --jq .number 2>/dev/null || true)
    [ -n "$own" ] && [ "$n" = "$own" ] && return 0
  fi
  # A CodeRabbit review request, on the pull request of the current branch only, with
  # exactly that body.
  review="(\"@coderabbitai (full )?review\"|'@coderabbitai (full )?review')"
  if printf '%s' "$cmd" | grep -Eq "^gh[[:space:]]+pr[[:space:]]+comment[[:space:]]+[0-9]+[[:space:]]+(--body|-b)[[:space:]]+${review}[[:space:]]*\$"; then
    n=$(printf '%s' "$cmd" | sed 's/^gh[[:space:]]*pr[[:space:]]*comment[[:space:]]*\([0-9]*\).*/\1/')
    own=$(cd "${cwd:-.}" 2>/dev/null && gh pr view --json number --jq .number 2>/dev/null || true)
    [ -n "$own" ] && [ "$n" = "$own" ] && return 0
  fi
  # A GraphQL read: one query in single quotes, with no single quote, backslash, $ or
  # backtick inside (double quotes are literal there), so the shell passes it as written and
  # "mutation" cannot be split or built;
  # optionally a --jq filter written the same way.
  sq="[^'\\\$\`]*"
  graphql="^gh[[:space:]]+api[[:space:]]+graphql[[:space:]]+-f[[:space:]]+query='${sq}'([[:space:]]+--jq[[:space:]]+'${sq}')?[[:space:]]*\$"
  if printf '%s' "$cmd" | grep -Eq "$graphql" && ! printf '%s' "$cmd" | grep -iq 'mutation'; then
    return 0
  fi
  return 1
}
# gh's --jq and --template can read the environment (env.GH_TOKEN): a gh command naming env
# asks, even a read the allow rules or the forms above let through.
# Quotes and backslashes are dropped first, so e'n'v or $''ENV still match.
if printf '%s' "$cmd" | grep -Eq '(^|[;&|(`[:space:]"'"'"'])gh[[:space:]]' &&
  printf '%s' "$cmd" | tr -d "\"'\\\\" | grep -Eq '(^|[^A-Za-z0-9_])(env|\$ENV)([^A-Za-z0-9_]|$)'; then
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: "This gh command names env, which a --jq or --template filter can use to print the environment and its tokens: the user confirms it explicitly."
    }
  }'
  exit 0
fi

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
# the e2e tasks create and delete a Kind cluster; acceptance-cleanup ends a release acceptance
# (the other acceptance tasks only read, or write test objects through the program's guard).
task_outbound="(task|make)[[:space:]]+${opts}(deploy|kind-deploy|undeploy|install|uninstall|docker-push|docker-buildx|run(:[a-z-]+)?|setup-test-e2e|test-e2e|cleanup-test-e2e|acceptance-cleanup)"
# The acceptance program run or built directly, around the tasks and the code they pin.
go_acceptance="go[[:space:]]+(run|build)[[:space:]][^;&|]*test/acceptance"
docker_push="docker[[:space:]]+${opts}((image|manifest)[[:space:]]+)?push|docker[[:space:]][^;&|]*--push(=[^[:space:]]*)?"
helm_outbound="helm[[:space:]]+${opts}(install|upgrade|uninstall|delete|rollback)"
kubectl_outbound="kubectl[[:space:]]+${opts}(apply|create|delete|replace|patch|edit|scale|rollout|drain|cordon|uncordon|taint|label|annotate|set|expose|autoscale|exec|cp|run|debug|attach)"
# A quote starts a command too: bash -c "git push" runs what the quotes hold. The commit
# messages are masked before matching, so text quoted there does not count.
pattern="(^|[;&|(\`[:space:]\"'])($git_push|$gh_outbound|$gh_api|$task_outbound|$go_acceptance|$docker_push|$helm_outbound|$kubectl_outbound)([[:space:]\"'\`]|\)|$)"

# The acceptance approval: its fingerprint script sits beside this hook, the one the settings run.
# When the session runs from the working copy under check, that is the copy of the branch, which a
# change to the hook or the script can rewrite: the approval only holds against a hook outside the
# change. The fixtures and the Markdown files are left out of what the user approves, as in the
# script. origin/main is always the remote-tracking ref, never a branch or tag of that name.
hook_dir=$(cd "$(dirname "$0")" && pwd)
approval=kuik-acceptance-approved
base=refs/remotes/origin/main
fixtures_spec=':(exclude,glob)test/acceptance/fixtures/*.yaml'
markdown_spec=':(exclude,glob)**/*.md'

ask() {
  jq -n --arg reason "$1" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: $reason
    }
  }'
  exit 0
}

# A commit message names commands without running them (fix(helm): let helm upgrade keep it):
# its quoted value is masked before matching, following the shell's quoting (mask-messages.sh).
. "$hook_dir/mask-messages.sh"
outbound_cmd=$(mask_messages "$cmd")

if printf '%s' "$outbound_cmd" | grep -Eq "$pattern"; then
  ask "This command leaves the working copy (push, pull request, image, release, deployment or cluster change): the user confirms it explicitly, see notes/0003."
fi

# The acceptance check below trusts the local origin/main: moving it by hand (update-ref,
# symbolic-ref, a replace object, a fetch into remotes/, another URL or refspec for origin) would
# let changed code run as if it were reviewed.
if printf '%s' "$cmd" | grep -Eq "(^|[;&|(\`[:space:]])git[[:space:]]+${git_opts}(update-ref|symbolic-ref|replace|fetch[[:space:]][^;&|]*remotes/|remote[[:space:]]+(set-url|add|rename|remove|rm|set-branches)|config[[:space:]][^;&|]*remote\.origin)"; then
  ask "This command moves a local ref the acceptance tasks trust: the user confirms it explicitly."
fi

# Only `task acceptance-approve`, which asks below, may write the approval: a command naming it
# could approve changed code without the user. Quotes and backslashes are dropped first, so a name
# split by them still matches.
unquoted=$(printf '%s' "$cmd" | tr -d "\"'\\\\")
case "$unquoted" in *"$approval"*)
  ask "This command touches the acceptance approval, which lets changed code reach a cluster: the user confirms it explicitly." ;;
esac
# git log, diff and show are allowed without asking, and --output writes their result to any
# path, a ref such as .git/refs/remotes/origin/main included.
case "$unquoted" in *refs/remotes* | *packed-refs* | *--output*)
  ask "This command may write a file, possibly a local ref the acceptance tasks trust: the user confirms it explicitly." ;;
esac

# A task run from another Taskfile or directory is not the task the allow rules name. Only what
# precedes `--` is task's own: the rest goes to the command the task runs. An option there may be
# a cluster (-tx.yml) or hidden by a quote or a backslash, so those ask too.
# Only the words of a task call count, up to the next ; & | or ): an option or a quote in another
# command of the line (go vet -tags, grep 'x') is not task's. A task call is found after any
# space, so a wrapper (timeout 600 task ...) cannot hide it; the commit messages are masked first,
# so the word task inside one does not count.
# The ` -- ` cut applies to each task call, not to the whole line, so `git log -- x; task -t ...`
# still asks.
if printf '%s' "$outbound_cmd" | grep -oE "(^|[;&|(\`[:space:]\"'])task([[:space:]][^;&|)]*)?" |
  sed 's/ -- .*//' |
  grep -Eq "[[:space:]]-[A-Za-z]*[tdg]|--taskfile|--dir|--global|[\"'\\\\]"; then
  ask "This task command may pick another Taskfile or directory: the user confirms it explicitly."
fi

# Approving lets the acceptance tasks run the program as it differs from origin/main, so it always
# asks, and the question lists what differs, from git rather than from the agent: the files
# changed with their line counts, and the new ones.
# The list is right only for `task acceptance-approve` alone, run from the top of the worktree
# whose approval it writes: any other form (a cd before it, a subdirectory) gets a question that
# lists nothing.
if printf '%s' "$cmd" | grep -Eq "(task|make)[[:space:]]+${opts}acceptance-approve([[:space:]]|\$)"; then
  top=$(git -C "${cwd:-.}" rev-parse --show-toplevel 2>/dev/null) || top=${cwd:-.}
  here=$(cd "${cwd:-.}" 2>/dev/null && pwd -P) || here=
  there=$(cd "$top" 2>/dev/null && pwd -P) || there=
  if ! printf '%s' "$cmd" | grep -Eq '^task[[:space:]]+acceptance-approve[[:space:]]*$' || [ -z "$here" ] || [ "$here" != "$there" ]; then
    ask "This command approves an acceptance program whose changes the hook cannot list: run \`task acceptance-approve\` alone, from the top of the worktree to approve, to see them. The user confirms this command explicitly."
  fi
  changed=$(git -C "$top" diff --no-ext-diff --no-renames --stat "$base" -- . "$fixtures_spec" "$markdown_spec" 2>/dev/null) || changed="(git diff failed)"
  untracked=$(git -C "$top" ls-files --others --exclude-standard -- . "$fixtures_spec" "$markdown_spec" 2>/dev/null) || untracked="(git ls-files failed)"
  ask "Approve the acceptance program as it differs from origin/main: the acceptance tasks then run it without asking, until it changes again. Read the agent's explanation of each change first.
Changed files:
${changed:-none}
New files:
${untracked:-none}"
fi

# The acceptance tasks reach a cluster without asking: check, inspect and validate because they
# only read, setup, apply and delete because the guard of test/acceptance keeps them on test
# objects. Both hold for the reviewed code only. Almost anything in
# the repository can change what runs (the program, the API types it decodes into, go.mod, a
# go.work, another Taskfile), so they pass only as one plain `task acceptance-X -- <args>` in a
# working copy equal to origin/main but for the fixtures and Markdown files: no other change, no
# other untracked file, no ignored file Go or Task would pick up. A fixture goes through the
# guard of origin/main object by object, and Markdown never runs, so a branch of new scenarios
# runs before its pull request. Any other change passes only while its fingerprint equals the one
# `task acceptance-approve` recorded in this worktree's git directory: one question for a whole
# run, asked again as soon as the code moves. Anything else, or a git error, asks. origin/main is
# the local ref: moving it takes a git command that asks on its own.
if printf '%s' "$cmd" | grep -Eq "(task|make)[[:space:]]+${opts}acceptance-(check|inspect|validate|setup|apply|delete)([[:space:]]|\$)"; then
  reason="This acceptance task reaches a cluster from code that differs from origin/main, or in a form the hook cannot check: the user confirms it explicitly."
  case "$cmd" in *'
'* | *"$(printf '\r')"*) ask "$reason" ;; esac
  # An argument is plain characters, or single-quoted pieces the shell reads literally (a regex
  # for --grep): only a single quote ends one, and none may appear inside.
  printf '%s' "$cmd" | grep -Eq "^task[[:space:]]+acceptance-(check|inspect|validate|setup|apply|delete)([[:space:]]+--([[:space:]]+([A-Za-z0-9=,./:_@+-]|'[^']*')+)*)?[[:space:]]*\$" || ask "$reason"
  top=$(git -C "${cwd:-.}" rev-parse --show-toplevel 2>/dev/null) || ask "$reason"
  # Task picks its Taskfile from the current directory: only the top level is the one checked.
  [ "$(cd "${cwd:-.}" 2>/dev/null && pwd -P)" = "$(cd "$top" && pwd -P)" ] || ask "$reason"
  # Git must see the tree as Go compiles it, against the reviewed origin/main: a plain
  # remote-tracking ref fetched from this repository, no replace object, no file hidden from the
  # index, no local attributes file a filter could hang on.
  git -C "$top" rev-parse -q --verify "$base^{commit}" >/dev/null 2>&1 || ask "$reason"
  if git -C "$top" symbolic-ref -q "$base" >/dev/null 2>&1; then ask "$reason"; fi
  [ -z "$(git -C "$top" for-each-ref refs/replace 2>/dev/null)" ] || ask "$reason"
  case "$(git -C "$top" config --get-all remote.origin.url 2>/dev/null)" in
    git@github.com:enix/kube-image-keeper.git | https://github.com/enix/kube-image-keeper.git | https://github.com/enix/kube-image-keeper) ;;
    *) ask "$reason" ;;
  esac
  [ "$(git -C "$top" config --get-all remote.origin.fetch 2>/dev/null)" = '+refs/heads/*:refs/remotes/origin/*' ] || ask "$reason"
  [ -z "$(git -C "$top" config --get-regexp '^(url\..*\.insteadof|core\.attributesfile)$' 2>/dev/null)" ] || ask "$reason"
  attributes=$(git -C "$top" rev-parse --path-format=absolute --git-path info/attributes 2>/dev/null) || ask "$reason"
  [ ! -s "$attributes" ] || ask "$reason"
  if git -C "$top" ls-files -v 2>/dev/null | grep -q '^[a-zS]'; then ask "$reason"; fi
  changed=$(git -C "$top" diff --no-ext-diff --no-renames --name-only "$base" 2>/dev/null) || ask "$reason"
  untracked=$(git -C "$top" ls-files --others --exclude-standard 2>/dev/null) || ask "$reason"
  if printf '%s\n%s\n' "$changed" "$untracked" |
    grep -Ev '^(test/acceptance/fixtures/[A-Za-z0-9._-]+\.yaml|[A-Za-z0-9._/-]+\.md)?$' | grep -q .; then
    unapproved="This acceptance task runs code that differs from origin/main and that the user has not approved as it is: run \`task acceptance-approve\` after explaining the changes, or the user confirms this command explicitly."
    git_dir=$(git -C "$top" rev-parse --absolute-git-dir 2>/dev/null) || ask "$unapproved"
    approved=$(cat "$git_dir/$approval" 2>/dev/null) || ask "$unapproved"
    current=$(cd "$top" && sh "$hook_dir/acceptance-fingerprint.sh" 2>/dev/null) || ask "$unapproved"
    [ -n "$current" ] && [ "$current" = "$approved" ] || ask "$unapproved"
  fi
  ignored=$(git -C "$top" ls-files --others --ignored --exclude-standard -- '*.go' test/acceptance go.work go.work.sum vendor ':(glob)**/[Tt]askfile*' 2>/dev/null) || ask "$reason"
  # git lists each worktree nested under .claude/worktrees/ as one ignored directory. Go skips
  # dot directories, and neither go nor task reads below the current one: drop them. Any file
  # listed under them, outside a nested repository, still asks.
  ignored=$(printf '%s\n' "$ignored" | grep -v '^\.claude/worktrees/[^/]*/$' || true)
  [ -z "$ignored" ] || ask "$reason"
fi
