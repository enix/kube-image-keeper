#!/usr/bin/env sh
# Prints the fingerprint of what the acceptance tasks would run beyond origin/main, from the
# working copy it runs in: the commit of origin/main, the diff of every file but the fixtures and
# the Markdown files, and the untracked files beside them, by name and content. The fixtures go
# through the guard and Markdown never runs, so they are left out. `task acceptance-approve`
# records it once the user has read the changes; confirm-outbound-actions.sh lets the acceptance
# tasks run while it holds.
#
# Git reads the tree as Go would compile it: no external diff, textconv or filter, no rename
# folding. Each part lands in a file under set -e before the hash, so a failing git command fails
# the script instead of hashing what came before it.
set -eu

cd "$(git rev-parse --show-toplevel)"
base=refs/remotes/origin/main
fixtures=':(exclude,glob)test/acceptance/fixtures/*.yaml'
markdown=':(exclude,glob)**/*.md'
parts=$(mktemp)
names=$(mktemp)
trap 'rm -f "$parts" "$names"' EXIT

git rev-parse --verify "$base^{commit}" >"$parts"
git diff --no-ext-diff --no-textconv --no-renames --binary "$base" -- . "$fixtures" "$markdown" >>"$parts"
git ls-files -z --others --exclude-standard -- . "$fixtures" "$markdown" >"$names"
cat "$names" >>"$parts"
if [ -s "$names" ]; then
  xargs -0 git hash-object --no-filters -- <"$names" >>"$parts"
fi
sha256sum <"$parts" | cut -d' ' -f1
