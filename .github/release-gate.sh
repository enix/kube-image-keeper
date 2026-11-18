#!/bin/sh
# release-gate.sh <version> <head sha> <ref>: refuses a stable release that did not run as a
# pre-release first. A patch X.Y.Z (Z > 0) needs a release candidate X.Y.Z-rc.N and is cut from
# its maintenance branch X.Y.x-stable, a minor X.Y.0 (Y > 0) needs a beta X.Y.0-beta.N, and the
# stable must be cut from the very commit of the last one, so what ships is what ran on
# clusters. A major and any pre-release are not gated. The tags are read from origin
# (git ls-remote), so a shallow checkout is enough.
# Exit 0: allowed. Exit 1: refused, with the reason. Exit 2: bad arguments, or the tags could
# not be read.
set -eu

version=${1:-}
head=${2:-}
ref=${3:-}
if ! printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'; then
  echo "release-gate: expected a version X.Y.Z or X.Y.Z-<pre-release>, got '$version'" >&2
  exit 2
fi
case "$head" in *[![:space:]]*) ;; *) echo "release-gate: expected the head commit sha" >&2; exit 2 ;; esac
case "$ref" in *[![:space:]]*) ;; *) echo "release-gate: expected the dispatched branch" >&2; exit 2 ;; esac

case "$version" in
  *-*) echo "release-gate: $version is a pre-release, not gated"; exit 0 ;;
esac

line=$(printf '%s' "$version" | cut -d. -f1-2)
minor=$(printf '%s' "$version" | cut -d. -f2)
patch=$(printf '%s' "$version" | cut -d. -f3)
if [ "$patch" != 0 ]; then
  pre=rc
  # Release candidates are published only on a maintenance line, so its patch is cut there.
  if [ "$ref" != "$line.x-stable" ]; then
    echo "::error::$version is a patch: it is released from $line.x-stable, not from $ref." >&2
    exit 1
  fi
elif [ "$minor" != 0 ]; then
  pre=beta
else
  echo "release-gate: $version is a major, not gated"
  exit 0
fi

# Each numbered tag of the pre-release with the commit it points to: the peeled ^{} line of an
# annotated tag wins over the tag object's own line.
if ! refs=$(git ls-remote --tags origin "refs/tags/v$version-$pre.*"); then
  echo "::error::could not read the tags of origin: retry the release, nothing was decided." >&2
  exit 2
fi
tags=$(printf '%s\n' "$refs" |
  awk -v prefix="v$version-$pre." '
    NF == 2 {
      name = $2; sub("^refs/tags/", "", name); peeled = sub("\\^\\{\\}$", "", name)
      n = substr(name, length(prefix) + 1)
      if (index(name, prefix) != 1 || n !~ /^(0|[1-9][0-9]*)$/) next
      if (peeled || !(name in sha)) sha[name] = $1
    }
    END { for (t in sha) print t, sha[t] }')
if [ -z "$tags" ]; then
  echo "::error::$version needs a $pre first: no tag v$version-$pre.N exists. Release v$version-$pre.1 and let it run on clusters before the stable release." >&2
  exit 1
fi

last=$(printf '%s\n' "$tags" | sort -V -k1,1 | tail -n 1)
name=${last% *}
sha=${last#* }
if [ "$sha" != "$head" ]; then
  echo "::error::$version must be cut from the commit of $name ($sha), the last $pre, not from $head. Move the branch to $name and dispatch again, or release a new $pre." >&2
  exit 1
fi
echo "release-gate: $version is cut from the commit of $name"
