#!/bin/sh
# Tells whether CodeRabbit has reviewed a pull request's own changes as they stand: the diff
# against main of its last reviewed commit must be the same patch as HEAD's. git patch-id
# --verbatim ignores line offsets, not whitespace, so a rebase that only brings main's changes
# keeps the patch and needs no new review. The reviewed commit is read from CodeRabbit's
# summary comment, which an incremental review updates ("between <old> and <new>") without
# posting a review, and else from its last review.
# Exits 0 when reviewed, 1 when not, 2 when it cannot tell. Run it from the worktree of the
# pull request. Usage: hack/coderabbit-reviewed.sh <PR number>
set -eu

pr=$1
repo=enix/kube-image-keeper
bot='coderabbitai[bot]'

summary=$(gh api "repos/$repo/issues/$pr/comments" --paginate --jq "[.[] | select(.user.login == \"$bot\") | .body | capture(\"between [0-9a-f]{40} and (?<sha>[0-9a-f]{40})\") | .sha] | last // empty") || exit 2
sha=$(printf '%s\n' "$summary" | grep -E '^[0-9a-f]{40}$' | tail -n 1 || true)
if [ -z "$sha" ]; then
	reviews=$(gh api "repos/$repo/pulls/$pr/reviews" --paginate --jq ".[] | select(.user.login == \"$bot\") | .commit_id") || exit 2
	sha=$(printf '%s\n' "$reviews" | grep -E '^[0-9a-f]{40}$' | tail -n 1 || true)
fi
if [ -z "$sha" ]; then
	echo "CodeRabbit has not reviewed #$pr yet"
	exit 1
fi

# A commit replaced by a force-push may be gone locally: GitHub still serves it by sha.
git cat-file -e "$sha^{commit}" 2>/dev/null || git fetch -q origin "$sha" || {
	echo "Cannot fetch the reviewed commit $sha" >&2
	exit 2
}

patch() {
	base=$(git merge-base origin/main "$1") || return 1
	# With its context lines: patch-id ignores line numbers, so without them an identical edit
	# moved elsewhere in a file would keep the patch. A rebase that changes a line next to the
	# PR's edits then asks for a review it did not need, the safe side.
	git diff "$base" "$1" | git patch-id --verbatim | cut -d' ' -f1
}
reviewed=$(patch "$sha") || exit 2
head=$(patch HEAD) || exit 2
if [ -z "$head" ]; then
	echo "HEAD has no change against main" >&2
	exit 2
fi
if [ "$reviewed" = "$head" ]; then
	echo "CodeRabbit reviewed the changes of #$pr as they stand (reviewed commit $sha)"
	exit 0
fi
echo "CodeRabbit last reviewed $sha, whose changes differ from HEAD's: ask for a review"
exit 1
