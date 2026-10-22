#!/bin/sh
# Takes one of the CodeRabbit reviews this machine may ask for, before a pull request opens or an
# `@coderabbitai review` is posted. The free OSS plan counts reviews per developer, so every
# session and worktree shares one quota: about 4 in a rolling hour. The ledger lives in the
# common git directory, seen by every worktree. One slot stays free for the reviews asked by
# hand outside the sessions.
# Exits 0 with the slot taken, 1 with the time the next one frees up, 2 on error.
# Usage: hack/coderabbit-slot.sh <what> (a PR number or a branch, written in the ledger)
set -eu

what=${1:?usage: hack/coderabbit-slot.sh <PR number or branch>}
limit=${CODERABBIT_SLOTS:-3}
window=${CODERABBIT_WINDOW:-3600}
ledger=$(git rev-parse --path-format=absolute --git-common-dir)/coderabbit-slots
lock=$ledger.lock

# mkdir is atomic: 2 sessions asking at once cannot both take the last slot.
tries=0
until mkdir "$lock" 2>/dev/null; do
	tries=$((tries + 1))
	[ "$tries" -lt 50 ] || {
		echo "Cannot lock $lock: remove it if no other session is taking a slot" >&2
		exit 2
	}
	sleep 0.1
done
trap 'rmdir "$lock"' EXIT

now=$(date -u +%s)
recent=$(mktemp)
trap 'rm -f "$recent"; rmdir "$lock"' EXIT
if [ -f "$ledger" ]; then
	awk -v since=$((now - window)) '$1 > since' "$ledger" >"$recent"
fi
used=$(wc -l <"$recent" | tr -d ' ')

if [ "$used" -lt "$limit" ]; then
	printf '%s %s\n' "$now" "$what" >>"$recent"
	cp "$recent" "$ledger"
	echo "CodeRabbit slot taken for $what ($((used + 1))/$limit in the last hour)"
	exit 0
fi

next=$(($(sort -n "$recent" | head -n 1 | cut -d' ' -f1) + window))
echo "No CodeRabbit slot free: the next one frees up at $(date -u -d "@$next" +%H:%M) UTC (in $(((next - now + 59) / 60)) min). Wait until then and run this again."
exit 1
