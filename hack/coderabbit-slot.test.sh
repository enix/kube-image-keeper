#!/bin/sh
# Checks hack/coderabbit-slot.sh in a throwaway repository: slots are taken up to the limit, an
# entry older than the window frees its slot, a full ledger names the next free time, and a
# worktree shares the ledger of its repository. Run by `task lint-hooks`.
set -eu

script=$(cd "$(dirname "$0")" && pwd)/coderabbit-slot.sh
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
git -C "$repo" init -q
git -C "$repo" -c user.name=check -c user.email=check@example.com commit -q --allow-empty -m init
git -C "$repo" worktree add -q "$repo/wt" -b wt
failed=0

check() { # expected exit code, directory, label
	set +e
	out=$(cd "$2" && CODERABBIT_SLOTS=2 sh "$script" "$3" 2>&1)
	code=$?
	set -e
	if [ "$code" != "$1" ]; then
		echo "FAIL: $3 exits $code, expected $1: $out"
		failed=1
	fi
}

check 0 "$repo" pr-1
check 0 "$repo/wt" pr-2
check 1 "$repo" pr-3
case "$out" in *'frees up at'*) ;; *)
	echo "FAIL: a full ledger names the next free time: $out"
	failed=1
	;;
esac

# An entry older than the window no longer counts.
printf '%s old\n' $(($(date -u +%s) - 4000)) >"$repo/.git/coderabbit-slots"
check 0 "$repo" pr-4
check 0 "$repo" pr-5
check 1 "$repo" pr-6

# A lock left behind is reported, never broken.
mkdir "$repo/.git/coderabbit-slots.lock"
check 2 "$repo" pr-7
rmdir "$repo/.git/coderabbit-slots.lock"

[ "$failed" = 0 ] && echo "all cases pass"
exit "$failed"
