#!/bin/sh
# Waits for the next CodeRabbit review on a pull request: exits 0 once a review is posted
# after the call, 1 after 60 minutes without one (a skipped review posts nothing).
# Usage: hack/wait-coderabbit.sh <PR number>
set -eu

pr=$1
repo=enix/kube-image-keeper

count() {
	gh api "repos/$repo/pulls/$pr/reviews" --paginate --jq '.[].user.login' | grep -c '^coderabbitai\[bot\]$' || true
}

before=$(count)
for _ in $(seq 120); do
	sleep 30
	if [ "$(count)" -gt "$before" ]; then
		echo "CodeRabbit posted a review on #$pr"
		exit 0
	fi
done
echo "No CodeRabbit review on #$pr after 60 minutes" >&2
exit 1
