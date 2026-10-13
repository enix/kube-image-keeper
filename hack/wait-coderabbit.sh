#!/bin/sh
# Waits for the next CodeRabbit review on a pull request, then for the CodeRabbit check to leave
# pending (it stays pending minutes after the review). A review is a new review posted after the
# call, or a new reviewed commit in CodeRabbit's summary comment: an incremental review may post
# no review at all. Exits 0 once reviewed, 3 as soon as CodeRabbit answers that it is rate
# limited (see the pull-request skill), 1 after 60 minutes without a review (a skipped review
# posts nothing) or when GitHub cannot be read at the start.
# Usage: hack/wait-coderabbit.sh <PR number>
set -eu

pr=$1
repo=enix/kube-image-keeper
bot='coderabbitai[bot]'

# Fails when gh fails: a count of 0 would take any earlier review for a new one.
reviews() {
	logins=$(gh api "repos/$repo/pulls/$pr/reviews" --paginate --jq '.[].user.login') || return 1
	printf '%s\n' "$logins" | grep -c '^coderabbitai\[bot\]$' || true
}

# The last commit the summary comment says it reviewed, empty when there is none yet.
summary() {
	shas=$(gh api "repos/$repo/issues/$pr/comments" --paginate --jq "[.[] | select(.user.login == \"$bot\") | .body | capture(\"between [0-9a-f]{40} and (?<sha>[0-9a-f]{40})\") | .sha] | last // empty") || return 1
	printf '%s\n' "$shas" | grep -E '^[0-9a-f]{40}$' | tail -n 1 || true
}

# Whether CodeRabbit posted a rate limit answer since the call.
rate_limited() {
	n=$(gh api "repos/$repo/issues/$pr/comments" --paginate --jq "[.[] | select(.user.login == \"$bot\" and .created_at > \"$start\") | select(.body | test(\"rate limit\"; \"i\"))] | length") || return 1
	[ "$(printf '%s\n' "$n" | awk '{ s += $1 } END { print s + 0 }')" -gt 0 ]
}

# The bucket of the CodeRabbit check: pass, fail, pending, skipping or cancel. gh exits non-zero
# while a check is pending, so its output decides; anything else, a missing check included, fails.
check() {
	bucket=$(gh pr checks "$pr" --repo "$repo" --json name,bucket --jq '.[] | select(.name == "CodeRabbit") | .bucket' 2>/dev/null) || true
	case "$bucket" in
		pass | fail | pending | skipping | cancel) printf '%s\n' "$bucket" ;;
		*) return 1 ;;
	esac
}

start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
before=$(reviews) || {
	echo "Cannot count the CodeRabbit reviews of #$pr" >&2
	exit 1
}
before_sha=$(summary) || {
	echo "Cannot read the CodeRabbit summary of #$pr" >&2
	exit 1
}
for _ in $(seq 120); do
	sleep 30
	if rate_limited; then
		echo "CodeRabbit is rate limited on #$pr: report it, never ask again in a loop" >&2
		exit 3
	fi
	now=$(reviews) || continue
	now_sha=$(summary) || continue
	if [ "$now" -gt "$before" ] || { [ -n "$now_sha" ] && [ "$now_sha" != "$before_sha" ]; }; then
		bucket=$(check) || continue
		if [ "$bucket" != pending ]; then
			echo "CodeRabbit reviewed #$pr, check: $bucket"
			exit 0
		fi
	fi
done
echo "No finished CodeRabbit review on #$pr after 60 minutes" >&2
exit 1
