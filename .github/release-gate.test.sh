#!/bin/sh
# Checks .github/release-gate.sh against a throwaway origin: tags on a few commits, and the exit
# code the script must give for a version and a head. Run by `task lint-hooks`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/release-gate.sh
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$dir/origin.git"
git clone -q "$dir/origin.git" "$dir/work" 2>/dev/null
repo=$dir/work
failed=0

# commit: an empty commit, its sha printed.
commit() {
  git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q --allow-empty -m "$1"
  git -C "$repo" rev-parse HEAD
}

# tag <name> <sha> [annotated]: a tag pushed to origin.
tag() {
  if [ "${3:-}" = annotated ]; then
    git -C "$repo" -c user.name=check -c user.email=check@example.com tag -a -m "$1" "$1" "$2"
  else
    git -C "$repo" tag "$1" "$2"
  fi
  git -C "$repo" push -q origin "$1"
}

# check <expected exit code> <version> <head sha> <case> [ref]: the ref defaults to the one a
# stable of that version is dispatched on.
check() {
  ref=${5:-}
  if [ -z "$ref" ]; then
    case "$2" in
      *.*.0 | *-*) ref=release ;;
      *) ref="$(printf '%s' "$2" | cut -d. -f1-2).x-stable" ;;
    esac
  fi
  set +e
  out=$(cd "$repo" && sh "$script" "$2" "$3" "$ref" 2>&1)
  code=$?
  set -e
  if [ "$code" != "$1" ]; then
    echo "FAIL: $4 exits $code, expected $1: $out"
    failed=1
  fi
}

a=$(commit "feat: a")
b=$(commit "feat: b")
c=$(commit "fix: c")
git -C "$repo" push -q origin HEAD:main

# A minor needs a beta, and ships from the commit of its last one.
tag v3.1.0-beta.1 "$a"
tag v3.1.0-beta.2 "$b" annotated
check 0 3.1.0 "$b" "a minor on the commit of its last beta, annotated tag"
check 1 3.1.0 "$c" "a minor on a commit after its last beta"
check 1 3.1.0 "$a" "a minor on the commit of an earlier beta"
check 1 3.2.0 "$b" "a minor with no beta"
check 1 3.1.0 "$b$b" "a head that is not a full sha of origin"

# A patch needs a release candidate, and ships from the commit of its last one; rc.10 comes
# after rc.9.
tag v3.0.1-rc.9 "$a"
tag v3.0.1-rc.10 "$c"
check 0 3.0.1 "$c" "a patch on the commit of its last rc"
check 1 3.0.1 "$a" "a patch on the commit of an earlier rc"
check 1 3.0.2 "$c" "a patch with no rc"
tag v3.0.2-beta.1 "$c"
check 1 3.0.2 "$c" "a patch with a beta but no rc"
tag v3.0.3-rc.preview "$c"
check 1 3.0.3 "$c" "a patch whose only rc tag is not numbered"
check 1 3.0.1 "$c" "a patch dispatched on release" release
check 1 3.0.1 "$c" "a patch dispatched on another line's stable branch" 3.1.x-stable

# A major and any pre-release are not gated.
check 0 4.0.0 "$c" "a major"
check 0 3.1.0-beta.3 "$c" "a beta"
check 0 3.0.1-rc.11 "$c" "an rc"
check 2 v3.1.0 "$b" "a version with its v prefix"
check 2 3junk.0.0 "$b" "a malformed version"
check 2 3.1.0 "$b" "no ref" " "

# The tags cannot be read: the lookup fails, not the gate.
git -C "$repo" remote set-url origin "$dir/missing.git"
check 2 3.1.0 "$b" "an unreachable origin"

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
