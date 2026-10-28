#!/bin/sh
# Checks the commit rules conform cannot express, on the commits since <base> (never those
# already on it). Run by `task check-commits` and by the Conform workflow on every pull request.
#   - The claude and e2e scopes take chore, docs, ci or test, never feat or fix: semantic-release
#     lists feat and fix in the release notes and bumps the version on them.
#   - A fix commit ends its body with `Introduced-by: #<PR>` (or `Introduced-by: unknown`): the
#     release notes tell a fix of a released version from a fix of a feature never released.
#     Dependabot's fix(deps) commits fix no bug and are exempt.
# Merge commits and fixup!, squash! and amend! commits are skipped: autosquash folds them.
# Usage: hack/check-commit-rules.sh <base>
set -eu

base=${1:?usage: hack/check-commit-rules.sh <base>}
failed=0
tooling=
untraced=

for c in $(git rev-list --no-merges "$base..HEAD"); do
  subject=$(git log -1 --format=%s "$c")
  case "$subject" in fixup!* | squash!* | amend!*) continue ;; esac
  line="$(git log -1 --format=%h "$c") $subject"
  if printf '%s' "$subject" | grep -Eq '^(feat|fix)\((claude|e2e)\)!?:'; then
    tooling="$tooling$line
"
  fi
  if printf '%s' "$subject" | grep -Eq '^fix(\([a-z0-9-]+\))?!?:'; then
    [ "$(git log -1 --format=%ae "$c")" = "49699333+dependabot[bot]@users.noreply.github.com" ] && continue
    last=$(git log -1 --format=%b "$c" | grep -v '^[[:space:]]*$' | tail -n 1)
    printf '%s' "$last" | grep -Eq '^Introduced-by: (#[0-9]+|unknown)$' || untraced="$untraced$line
"
  fi
done

if [ -n "$tooling" ]; then
  echo "The claude and e2e scopes take chore, docs, ci or test, never feat or fix:" >&2
  printf '%s' "$tooling" >&2
  failed=1
fi
if [ -n "$untraced" ]; then
  echo "A fix commit ends its body with 'Introduced-by: #<PR>', the pull request that introduced the bug (or 'Introduced-by: unknown', the user agreeing):" >&2
  printf '%s' "$untraced" >&2
  failed=1
fi
exit "$failed"
