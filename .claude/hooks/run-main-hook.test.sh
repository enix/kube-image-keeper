#!/bin/sh
# Checks run-main-hook.sh in a throwaway repository: which version of a hook it runs.
set -u
launcher="$(cd "$(dirname "$0")" && pwd)/run-main-hook.sh"
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
git -C "$repo" init -q -b main
mkdir -p "$repo/.claude/hooks"
cp "$launcher" "$repo/.claude/hooks/run-main-hook.sh"
printf '#!/bin/sh\necho main "$@"\ncat\n' >"$repo/.claude/hooks/probe.sh"
chmod +x "$repo/.claude/hooks/probe.sh"
git -C "$repo" add -A
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -m main
git -C "$repo" update-ref refs/remotes/origin/main HEAD
# The working copy differs from origin/main, and holds a hook origin/main does not have yet.
printf '#!/bin/sh\necho live "$@"\ncat\n' >"$repo/.claude/hooks/probe.sh"
printf '#!/bin/sh\necho new\n' >"$repo/.claude/hooks/new.sh"
chmod +x "$repo/.claude/hooks/new.sh"
failed=0

check() {
  want=$1
  shift
  got=$(echo input | "$@" 2>/dev/null | tr '\n' ' ')
  if [ "$got" != "$want" ]; then
    echo "FAIL: want '$want', got '$got': $*"
    failed=1
  fi
}

run="$repo/.claude/hooks/run-main-hook.sh"
# origin/main's hook runs, with its arguments and the tool call on stdin.
check "main --outline input " "$run" probe.sh --outline
# Twice: the second call reads the extracted copy.
check "main input " "$run" probe.sh
check "live input " env KUIK_LIVE_HOOKS=1 "$run" probe.sh
check "new " "$run" new.sh
check "" "$run" ../probe.sh
# A new origin/main commit is extracted on its own: the hook changed there runs.
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -m change -- .claude/hooks/probe.sh
git -C "$repo" update-ref refs/remotes/origin/main HEAD
printf '#!/bin/sh\necho edited\n' >"$repo/.claude/hooks/probe.sh"
check "live input " "$run" probe.sh
# Without origin/main, the working copy's hook runs.
git -C "$repo" update-ref -d refs/remotes/origin/main
check "edited " "$run" probe.sh

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
