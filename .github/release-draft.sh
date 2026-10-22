#!/usr/bin/env bash
# The draft release that carries the notes written for a release, from the beta on
# (CONTRIBUTING.md, "Release notes"). Used by the Release workflow:
#
#   release-draft.sh find <tag>      print the id of the draft release for <tag>; fail, naming
#                                    the drafts that exist, when there is none or several
#   release-draft.sh source <id> <dest>
#                                    download the release-notes.yaml asset of release <id>,
#                                    the single source of the notes, to <dest>; fail when it
#                                    is missing or empty
#
# The release body and the chart's artifacthub.io/changes are rendered from that source with
# go run ./hack/releasenotes; the draft's own body is only a preview, never read.
# The commands call the GitHub API with gh: GH_TOKEN must have push access to see drafts.
set -euo pipefail

repo="${GITHUB_REPOSITORY:-enix/kube-image-keeper}"
source_name="release-notes.yaml"

error() {
  echo "::error::$*" >&2
  exit 1
}

find_draft() {
  local tag="$1" drafts ids names
  drafts=$(gh api --paginate "repos/${repo}/releases" --jq '.[] | select(.draft) | "\(.id) \(.tag_name)"')
  ids=$(awk -v tag="$tag" '$2 == tag { print $1 }' <<<"$drafts")
  names=$(awk '{ print $2 }' <<<"$drafts" | sort -u | paste -sd ' ')
  case $(wc -w <<<"$ids") in
    1) echo "$ids" ;;
    0) error "No draft release for ${tag}, the version semantic-release computes. Draft releases: ${names:-none}. Write the notes of ${tag} with the release-notes skill, or check the commits if ${tag} is not the version you expected." ;;
    *) error "Several draft releases for ${tag}: delete all but the one that holds the notes." ;;
  esac
}

# A draft has no tag yet, so the asset is fetched by its id rather than with gh release download.
draft_source() {
  local asset
  asset=$(gh api "repos/${repo}/releases/$1/assets" --jq ".[] | select(.name == \"${source_name}\") | .id")
  [[ -n "$asset" ]] || error "The draft release $1 has no ${source_name} asset: upload the source of its notes with gh release upload (the release-notes skill)."
  gh api -H "Accept: application/octet-stream" "repos/${repo}/releases/assets/${asset}" >"$2"
  [[ -n "$(tr -d '[:space:]' <"$2")" ]] || error "The ${source_name} asset of the draft release $1 is empty."
}

case "${1:-}" in
  find) find_draft "$2" ;;
  source) draft_source "$2" "${3:?usage: $0 source <id> <dest>}" ;;
  *) error "usage: $0 find <tag> | source <id> <dest>" ;;
esac
