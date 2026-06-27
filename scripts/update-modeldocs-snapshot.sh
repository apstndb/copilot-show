#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
snapshot_dir="$repo_root/pkg/modeldocs/snapshot"
docs_repo_url="https://github.com/github/docs"
docs_ref="${1:-main}"

mkdir -p "$snapshot_dir"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

files=(
  model-release-status.yml
  model-supported-clients.yml
  model-supported-plans.yml
  model-comparison.yml
  model-deprecation-history.yml
)

resolve_commit() {
  local ref=$1

  if [[ $ref =~ ^[0-9a-f]{40}$ ]]; then
    printf '%s\n' "$ref"
    return 0
  fi

  local commit
  commit=$(git ls-remote "$docs_repo_url.git" "refs/heads/$ref" | awk 'NR == 1 { print $1 }')
  if [[ -z "$commit" ]]; then
    commit=$(git ls-remote "$docs_repo_url.git" "$ref" | awk 'NR == 1 { print $1 }')
  fi
  if [[ -z "$commit" ]]; then
    echo "Unable to resolve github/docs ref: $ref" >&2
    exit 1
  fi
  printf '%s\n' "$commit"
}

commit=$(resolve_commit "$docs_ref")
base_url="https://raw.githubusercontent.com/github/docs/$commit/data/tables/copilot"

for file in "${files[@]}"; do
  curl -fsSL "$base_url/$file" -o "$tmp_dir/$file"
done

cat > "$tmp_dir/source.json" <<JSON
{
  "repo": "$docs_repo_url",
  "ref": "$docs_ref",
  "commit": "$commit",
  "snapshotPath": "pkg/modeldocs/snapshot"
}
JSON

for file in "${files[@]}"; do
  mv "$tmp_dir/$file" "$snapshot_dir/$file"
done
mv "$tmp_dir/source.json" "$snapshot_dir/source.json"

echo "Updated modeldocs snapshot from github/docs $docs_ref ($commit)"
