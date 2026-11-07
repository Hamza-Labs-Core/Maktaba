#!/bin/bash
# Merge origin/main into the current branch, keeping both sides of CHANGELOG.md and
# CHANGELOG.ar.md, which conflict on almost every merge because every pull request adds an
# entry at the end of the same section.
#
#   tools/dev/merge-main.sh [<ref>]     (default origin/main; fetched first)
#
# A conflicted CHANGELOG is merged entry by entry from git's three stages
# (tools/dev/changelog_merge.py): main's file, with this branch's new entries at the end of
# their sections and its edits of existing entries, never line by line (a line-wise merge,
# like git's own or `merge=union`, splits two new entries that share a line and interleaves
# them). Any other conflicted file, or a CHANGELOG change it cannot place, stops it with the
# merge left in progress for a person (exit 3). A merge already in progress (the branch's
# own, after fixing other files) is finished the same way.
#
# Exit codes: 0 merged (or already up to date), 3 a person must resolve, 1 an error.
# bash 3.2 (macOS): no mapfile, no associative arrays.
set -euo pipefail

ref="${1:-origin/main}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
top="$(git rev-parse --show-toplevel)"
cd "$top"

if [ ! -f "$(git rev-parse --git-path MERGE_HEAD)" ]; then
  if [ "$ref" = origin/main ]; then
    git fetch -q origin main
  fi
  if git merge --no-edit "$ref" >/dev/null 2>&1; then
    echo "merge-main: merged $ref cleanly"
    exit 0
  fi
  if [ ! -f "$(git rev-parse --git-path MERGE_HEAD)" ]; then
    echo "merge-main: git merge $ref failed before any conflict (see git status)" >&2
    exit 1
  fi
fi

conflicted=()
n=0
while IFS= read -r f; do
  if [ -n "$f" ]; then
    conflicted[n]="$f"
    n=$((n + 1))
  fi
done < <(git diff --name-only --diff-filter=U)

others=""
for f in ${conflicted[@]+"${conflicted[@]}"}; do
  case "$f" in
    CHANGELOG.md | CHANGELOG.ar.md) ;;
    *) others="$others $f" ;;
  esac
done
if [ -n "$others" ]; then
  echo "merge-main: conflicts a person must resolve:$others" >&2
  echo "merge-main: resolve them, \`git add\` them, then run this again to finish the CHANGELOGs" >&2
  exit 3
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for f in ${conflicted[@]+"${conflicted[@]}"}; do
  git show ":1:$f" >"$tmp/base" 2>/dev/null || : >"$tmp/base"
  git show ":2:$f" >"$tmp/ours"
  git show ":3:$f" >"$tmp/theirs"
  if ! python3 "$here/changelog_merge.py" "$tmp/base" "$tmp/ours" "$tmp/theirs" "$tmp/out"; then
    echo "merge-main: $f cannot be merged by entries: resolve it by hand, then run this again" >&2
    exit 3
  fi
  cp "$tmp/out" "$f"
  git add -- "$f"
done

for f in ${conflicted[@]+"${conflicted[@]}"}; do
  if grep -q -e '^<<<<<<< ' -e '^>>>>>>> ' "$f"; then
    echo "merge-main: conflict markers are left in $f" >&2
    exit 3
  fi
done
git commit -q --no-edit
if [ "$n" -gt 0 ]; then
  echo "merge-main: merged $ref; kept both sides of:${conflicted[*]+ ${conflicted[*]}}"
else
  echo "merge-main: merged $ref"
fi
