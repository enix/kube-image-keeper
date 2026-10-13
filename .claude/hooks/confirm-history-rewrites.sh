#!/usr/bin/env sh
# PreToolUse hook (Bash): commands that rewrite existing commits (start a rebase, amend,
# reset onto a commit, move a branch, filter-branch) ask the user first. Not forbidden: a
# branch is rewritten as rarely as possible, and the user decides when (the autosquash of
# the pull-request skill).
# git commit --fixup, plain commits and the steps of a rebase already started (--continue,
# --skip, --abort, --quit) stay allowed. The ask rules of settings.json back it up.
set -eu

command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the command and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
[ -n "$cmd" ] || exit 0
cwd=$(printf '%s' "$input" | jq -r '.cwd // "."')

# The git subcommand is the first word that is not an option, nor the value of one of the
# few options that take it as a separate word, as in confirm-outbound-actions.sh.
git_opts='((-C|-c|--git-dir|--work-tree|--namespace|--config-env)[[:space:]]+[^[:space:]]+[[:space:]]+|-[^[:space:]]*[[:space:]]+)*'
git="git[[:space:]]+${git_opts}"
rebase="${git}rebase"
# The steps of a rebase already started rewrite nothing the user has not confirmed: drop
# them before matching, so `git add . && git rebase --continue` passes.
rebase_step="${rebase}[[:space:]]+--(continue|skip|abort|quit|edit-todo|show-current-patch)([[:space:]]|$)"
amend="${git}commit[[:space:]][^;&|]*--amend"
pull_rebase="${git}pull[[:space:]][^;&|]*(--rebase|-r)"
move_branch="${git}(branch[[:space:]][^;&|]*(-f|--force|-M|-C)|checkout[[:space:]][^;&|]*-B|switch[[:space:]][^;&|]*(-C|--force-create)|update-ref|filter-branch|filter-repo|replace)"
pattern="(^|[;&|(\`[:space:]])(${rebase}|${amend}|${pull_rebase}|${move_branch})([[:space:]=]|\)|$)"

# git reset moves the branch when it names a mode, or a commit-ish alone (HEAD~2, a sha, a
# branch name); git reset [<commit-ish>] [--] <path>... only unstages. The first word that
# is not an option decides: an existing path unstages, anything else is a commit-ish, which
# moves the branch unless paths follow it.
reset_moves_branch() {
  printf '%s\n' "$cmd" | grep -oE "(^|[;&|(\`[:space:]])${git}reset([[:space:]]+[^;&|[:space:]]+)*" |
    while IFS= read -r words; do
      set -f
      # shellcheck disable=SC2086 # split the words on purpose
      set -- $words
      set +f
      while [ "$#" -gt 0 ] && [ "$1" != reset ]; do shift; done
      [ "$#" -gt 0 ] && shift
      target=
      for word; do
        case $word in
          --soft | --mixed | --hard | --keep | --merge) echo moves && break ;;
          *'>'* | *'<'*) continue ;; # a redirection
          --) [ -z "$target" ] || target=paths && break ;;
          -*) continue ;;
        esac
        if [ -n "$target" ]; then target=paths && break; fi
        word=${word#[\"\']} && word=${word%[\"\']}
        case $word in /*) path=$word ;; *) path=$cwd/$word ;; esac
        if [ -e "$path" ]; then target=paths && break; fi
        target=$word
      done
      [ -z "$target" ] || [ "$target" = paths ] || echo moves
    done | grep -q moves
}

if printf '%s' "$cmd" | sed -E "s/${rebase_step}/ /g" | grep -Eq "$pattern" || reset_moves_branch; then
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: "This command rewrites existing commits (rebase, amend, reset onto a commit, branch move): the user confirms it explicitly. Fix a commit with git commit --fixup instead; a branch is autosquashed as rarely as possible, see the pull-request skill."
    }
  }'
fi
