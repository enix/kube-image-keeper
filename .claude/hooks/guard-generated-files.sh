#!/usr/bin/env sh
# PreToolUse hook (Edit|Write): refuse hand edits of generated files and point to the
# task that regenerates them. The generated files are the paths .gitattributes marks with
# generated-by=<task>. A Bash command that writes a file (sed -i, >) is not covered.
set -eu

command -v git >/dev/null 2>&1 || {
  echo "git is missing: this hook cannot check the edit and refuses it" >&2
  exit 2
}
command -v jq >/dev/null 2>&1 || {
  echo "jq is missing: this hook cannot check the edit and refuses it. Install jq (https://jqlang.org/download/) to develop on this repository" >&2
  exit 2
}
path=$(jq -r '.tool_input.file_path // empty')
[ -n "$path" ] || exit 0

# A path outside the repository makes check-attr fail: it is not a generated file.
by=$(git -C "${CLAUDE_PROJECT_DIR:-.}" check-attr generated-by -- "$path" 2>/dev/null |
  sed -n 's/.*: generated-by: //p')
case "$by" in
  "" | unspecified | unset | set) exit 0 ;;
  kubebuilder) task="the kubebuilder CLI (kubebuilder create api / create webhook)" ;;
  generate-helm-docs) task="task generate-helm-docs (edit the # -- comments of values.yaml or README.md.gotmpl instead)" ;;
  *) task="task $by" ;;
esac

jq -n --arg reason "$path is generated, never edit it by hand: edit the source (kubebuilder markers, values.yaml comments) and run $task" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    permissionDecision: "deny",
    permissionDecisionReason: $reason
  }
}'
