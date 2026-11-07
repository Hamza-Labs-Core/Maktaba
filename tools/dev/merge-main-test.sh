#!/bin/bash
# Tests of tools/dev/merge-main.sh, each in a throwaway repository (bash 3.2: macOS's).
#
#   bash tools/dev/merge-main-test.sh
#
# Both sides add an entry at the end of a section; two new entries share a line (which a
# line-wise merge interleaves); the Arabic CHANGELOG; this branch edits an entry while main
# adds one; both edit the same entry (stops); a conflict outside the CHANGELOGs (stops, the
# merge left in progress); main adding more entries than the branch; a merge with no conflict.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
failures=0
CASE=""

fail() {
  echo "FAIL: $CASE: $*" >&2
  failures=$((failures + 1))
}

# repo NAME: a repository with merge-main.sh and its helper where the tool looks for them,
# on branch main with one commit
repo() {
  local d="$work/$1"
  mkdir -p "$d/tools/dev"
  cp "$here/merge-main.sh" "$here/changelog_merge.py" "$d/tools/dev/"
  cd "$d"
  git init -q
  git checkout -q -b main
  git config user.email test@example.invalid
  git config user.name test
  git config commit.gpgsign false
  git add -A
  git commit -qm tools
}

# put BRANCH FILE: commit stdin as FILE on BRANCH (a new branch starts at main)
put() {
  if git rev-parse -q --verify "refs/heads/$1" >/dev/null; then
    git checkout -q "$1"
  else
    git checkout -q -b "$1" main
  fi
  cat >"$2"
  git add -A
  git commit -qm "$1: $2"
}

# run BRANCH: merge-main.sh main from BRANCH; prints its exit code (its output in $work/out)
run() {
  git checkout -q "$1"
  local code=0
  bash tools/dev/merge-main.sh main >"$work/out" 2>&1 || code=$?
  echo "$code"
}

# same FILE: FILE holds stdin exactly
same() {
  cat >"$work/want"
  if ! cmp -s "$1" "$work/want"; then
    fail "$1 is not what it should be"
    diff "$work/want" "$1" >&2 || true
  fi
}

HEAD_EN='# Changelog

## [Unreleased]

### Added

- **Old entry.** Its first line
  and its second line.
'
TAIL_EN='
### Fixed

- **A fix.**
'
en() { printf '%s%s%s' "$HEAD_EN" "$1" "$TAIL_EN"; }

# 1. both sides add an entry at the end of Added
CASE="both sides add"
repo add
en "" | put main CHANGELOG.md
en '
- **Entry of the branch.** Its first line
  and its tail.
' | put topic CHANGELOG.md
en '
- **Entry of main.** Its first line
  and another tail.
' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
en '
- **Entry of main.** Its first line
  and another tail.

- **Entry of the branch.** Its first line
  and its tail.
' | same CHANGELOG.md
[ -z "$(git status --porcelain)" ] || fail "the tree is not clean after the merge"
if git rev-parse -q --verify MERGE_HEAD >/dev/null; then fail "the merge is not committed"; fi

# 2. two new entries share their last line (git leaves it outside the conflict)
CASE="entries share a line"
repo shared
en "" | put main CHANGELOG.md
en '
- **Entry B.** About b
  maktaba.dev: the guide.
' | put topic CHANGELOG.md
en '
- **Entry A.** About a
  maktaba.dev: the guide.
' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
en '
- **Entry A.** About a
  maktaba.dev: the guide.

- **Entry B.** About b
  maktaba.dev: the guide.
' | same CHANGELOG.md

# 3. the Arabic CHANGELOG
CASE="Arabic"
HEAD_AR='# سجل التغييرات

## [لم يصدر بعد]

### أُضيف

- **إدخال قديم.** سطره الأول
  وسطره الثاني.
'
TAIL_AR='
### أُصلح

- **إصلاح.**
'
ar() { printf '%s%s%s' "$HEAD_AR" "$1" "$TAIL_AR"; }
repo arabic
ar "" | put main CHANGELOG.ar.md
ar '
- **إدخال الفرع.** عن الفرع
  موقع maktaba.dev: الدليل.
' | put topic CHANGELOG.ar.md
ar '
- **إدخال الرئيسي.** عن الرئيسي
  موقع maktaba.dev: الدليل.
' | put main CHANGELOG.ar.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
ar '
- **إدخال الرئيسي.** عن الرئيسي
  موقع maktaba.dev: الدليل.

- **إدخال الفرع.** عن الفرع
  موقع maktaba.dev: الدليل.
' | same CHANGELOG.ar.md

# 4. this branch edits an entry while main adds one after it
CASE="an edit beside an addition"
repo edit
en "" | put main CHANGELOG.md
en "" | sed 's/and its second line\./and its second line (migration 0208)./' | put topic CHANGELOG.md
en '
- **Entry of main.** About main.
' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
en '
- **Entry of main.** About main.
' | sed 's/and its second line\./and its second line (migration 0208)./' | same CHANGELOG.md

# 5. both sides edit the same entry: a person decides
CASE="both edit one entry"
repo both
en "" | put main CHANGELOG.md
en "" | sed 's/and its second line\./and the second line of the branch./' | put topic CHANGELOG.md
en "" | sed 's/and its second line\./and the second line of main./' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 3 ] || fail "exit $code, want 3: $(cat "$work/out")"
git rev-parse -q --verify MERGE_HEAD >/dev/null || fail "the merge is not left in progress"
grep -q "cannot be merged by entries" "$work/out" || fail "no reason given: $(cat "$work/out")"

# 6. a conflict outside the CHANGELOGs stops it, the merge left in progress
CASE="another file"
repo other
en "" | put main CHANGELOG.md
echo base | put main README.md
echo "the branch" | put topic README.md
en '
- **Entry of the branch.** x
' | put topic CHANGELOG.md
echo main | put main README.md
en '
- **Entry of main.** y
' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 3 ] || fail "exit $code, want 3: $(cat "$work/out")"
grep -q "README.md" "$work/out" || fail "README.md not named: $(cat "$work/out")"
git rev-parse -q --verify MERGE_HEAD >/dev/null || fail "the merge is not left in progress"
grep -q '^<<<<<<< ' CHANGELOG.md || fail "the CHANGELOG was resolved while another file waits"

# 8. main adds more entries than this branch: the blank lines that separate entries differ
# in number on the two sides, and are not a change of the text between them
CASE="main adds more"
repo more
en "" | put main CHANGELOG.md
en '
- **Entry of the branch.** x
' | put topic CHANGELOG.md
en '
- **Entry A of main.** a

- **Entry B of main.** b
' | put main CHANGELOG.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
en '
- **Entry A of main.** a

- **Entry B of main.** b

- **Entry of the branch.** x
' | same CHANGELOG.md

# 7. no conflict: merged, nothing else touched
CASE="clean"
repo clean
en "" | put main CHANGELOG.md
echo notes | put main NOTES.md
echo other | put topic OTHER.md
code="$(run topic)"
[ "$code" = 0 ] || fail "exit $code: $(cat "$work/out")"
{ [ -f NOTES.md ] && [ -f OTHER.md ]; } || fail "the merge lost a file"

if [ "$failures" -gt 0 ]; then
  echo "merge-main-test: $failures failed" >&2
  exit 1
fi
echo "merge-main-test: every case passed"
