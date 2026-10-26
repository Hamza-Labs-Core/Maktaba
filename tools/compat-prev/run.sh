#!/usr/bin/env bash
# compat-prev (Epic 35 story 35.5 AC6, and the machines' skew of design §9.3): the
# previous stable release against this build's database, both ways.
#
#  1. The previous release's code on this build's schema: its repository tests (the API's
#     real-Postgres packages) run with this build's migrations in place of its own. An
#     automatic update's rollback runs the previous release on the database the new one
#     migrated (migrations are additive), so a failure fails the build — unless a
#     migration newer than that release is marked "Breaking for the previous release" in
#     MANIFEST.md (such a release is held for an admin, story 35.3).
#  2. A machine behind its home: the previous release's node schema guard reads a
#     database this build migrated, and pauses exactly when this build's
#     maktaba_min_node_schema() is above the previous release's schema.
#  3. A machine ahead of its home: this build's guard reads a database the previous
#     release migrated, and pauses exactly when that database is below this build's
#     schema ("update the home").
#
# "An old node against a new home" is covered at the schema level: the old release's
# code and guard on the new database. No old node process talks to a new home here (the
# cluster channel's and the link's protocols are versioned and checked by their own
# tests).
#
# Needs MAKTABA_TEST_PG_URL (a Postgres whose user may create databases and roles: it
# makes and drops its own compat_* databases, and the migrations' cluster-wide roles),
# psql (PSQL to name another), git tags (a full clone), and Go. COMPAT_PREV_TAG names
# the release to compare with (default: the newest stable tag before HEAD).
set -euo pipefail

ROOT=$(git rev-parse --show-toplevel)
PG=${MAKTABA_TEST_PG_URL:-}
PSQL=${PSQL:-psql}
if [ -z "$PG" ]; then
  echo "compat-prev: set MAKTABA_TEST_PG_URL (a Postgres whose user may create databases and roles)" >&2
  exit 2
fi

# the newest stable tag that is not HEAD itself
prev_tag() {
  local head t
  head=$(git -C "$ROOT" rev-parse HEAD)
  git -C "$ROOT" tag --merged HEAD --list 'v[0-9]*' --sort=-version:refname |
    grep -Ev -- '-(rc|alpha|beta|a|b)\.?[0-9]*$' |
    while read -r t; do
      if [ "$(git -C "$ROOT" rev-list -n1 "$t")" != "$head" ]; then
        echo "$t"
        break
      fi
    done
}

PREV=${COMPAT_PREV_TAG:-$(prev_tag)}
if [ -z "$PREV" ]; then
  echo "compat-prev: no stable release before HEAD: nothing to compare with"
  exit 0
fi

WORK=$(mktemp -d)
RUN=$$
NEW_DB="compat_new_$RUN"
OLD_DB="compat_old_$RUN"

db_url() { # the URL of database $1 on the server of MAKTABA_TEST_PG_URL
  printf '%s\n' "$PG" | sed -E "s#^(postgres(ql)?://[^/]+)/[^?]*#\\1/$1#"
}
sql() { "$PSQL" -X -q -v ON_ERROR_STOP=1 -d "$PG" -c "$1" >/dev/null; }

cleanup() {
  cd "$ROOT"
  git -C "$ROOT" worktree remove --force "$WORK/prev" >/dev/null 2>&1 || true
  rm -rf "$WORK" "$ROOT/api/internal/compatprobe"
  sql "DROP DATABASE IF EXISTS $NEW_DB" 2>/dev/null || true
  sql "DROP DATABASE IF EXISTS $OLD_DB" 2>/dev/null || true
}
trap cleanup EXIT

git -C "$ROOT" worktree add --detach --quiet "$WORK/prev" "$PREV"
PREV_DIR="$WORK/prev"

newest_slot() { # the newest Postgres migration slot of directory $1
  local s
  s=$(ls "$1" | grep -E '^[0-9]{4}_.*\.sql$' | grep -v '\.sqlite\.sql$' | cut -c1-4 | sort | tail -1)
  echo $((10#${s:-0}))
}
PREV_TOP=$(newest_slot "$PREV_DIR/shared/db/migrations")
HEAD_TOP=$(newest_slot "$ROOT/shared/db/migrations")
echo "compat-prev: $PREV (schema $PREV_TOP) against this build (schema $HEAD_TOP)"

# migrations newer than the previous release that say they break it
breaking=""
while IFS= read -r line; do
  slot=$(printf '%s\n' "$line" | sed -E 's/^\| *`([0-9]{4})`.*/\1/')
  if [ $((10#$slot)) -gt "$PREV_TOP" ] && printf '%s\n' "$line" | grep -q "Breaking for the previous release"; then
    breaking="$breaking $slot"
  fi
done < <(grep -E '^\| *`[0-9]{4}`' "$ROOT/shared/db/migrations/MANIFEST.md")

# ---------- the machines' skew (2, 3) ----------

sql "CREATE DATABASE $NEW_DB"
sql "CREATE DATABASE $OLD_DB"
migrate() { # $1 a checkout, $2 a database: that checkout's migrations applied (quiet unless they fail)
  local out
  if ! out=$(cd "$1/api" && DATABASE_URL=$(db_url "$2") go run . migrate up --dir "$1/shared/db/migrations" 2>&1); then
    printf '%s\n' "$out" >&2
    return 1
  fi
}
echo "==> $OLD_DB migrated by $PREV"
migrate "$PREV_DIR" "$OLD_DB"
echo "==> $NEW_DB migrated by this build"
migrate "$ROOT" "$NEW_DB"

probe() { # $1 a checkout, $2 a database: that checkout's node schema guard's verdict
  mkdir -p "$1/api/internal/compatprobe"
  cp "$ROOT/tools/compat-prev/schemaprobe.go.txt" "$1/api/internal/compatprobe/main.go"
  (cd "$1/api" && go run ./internal/compatprobe "$(db_url "$2")")
}
field() { printf '%s\n' "$1" | sed -n "s/^$2=//p"; }

skew_failed=0
if [ -f "$PREV_DIR/api/internal/cluster/schemaguard/guard.go" ]; then
  out=$(probe "$PREV_DIR" "$NEW_DB")
  rev=$(field "$out" rev)
  min=$(field "$out" min_node)
  pause=$(field "$out" pause)
  want=no
  [ "${min:-0}" -gt "$rev" ] && want=yes
  got=no
  [ -n "$pause" ] && got=yes
  echo "==> a $PREV node (schema $rev) on this build's home (min node schema ${min:-0}): pauses=$got (want $want) ${pause}"
  [ "$got" = "$want" ] || skew_failed=1
else
  echo "==> $PREV has no node schema guard: the machine-behind check is skipped"
fi
out=$(probe "$ROOT" "$OLD_DB")
rev=$(field "$out" rev)
applied=$(field "$out" applied)
pause=$(field "$out" pause)
want=no
[ "$applied" -lt "$rev" ] && want=yes
got=no
[ -n "$pause" ] && got=yes
echo "==> a node of this build (schema $rev) on a $PREV home (at $applied): pauses=$got (want $want) ${pause}"
[ "$got" = "$want" ] || skew_failed=1
if [ "$skew_failed" -ne 0 ]; then
  echo "compat-prev: a node's schema guard did not follow the min_node_schema rule (design §9.3)" >&2
  exit 1
fi

# ---------- the previous release's code on this build's schema (1) ----------

rm -rf "$PREV_DIR/shared/db/migrations"
cp -R "$ROOT/shared/db/migrations" "$PREV_DIR/shared/db/migrations"
cd "$PREV_DIR/api"
pkgs=$(grep -rlE 'MAKTABA_TEST_PG_URL|clustertest\.ScratchDB' --include='*_test.go' internal |
  xargs -n1 dirname | sort -u | sed 's#^#./#')
# the tests that assert the release's own newest migration (its SchemaRev) say nothing of
# compatibility: the newer migrations make them fail by design
skip='^(TestSchemaRevIsTheHighestMigration)$'
echo "==> $PREV's repository tests on this build's migrations ($(echo "$pkgs" | wc -l | tr -d ' ') packages)"
set +e
# shellcheck disable=SC2086 # one argument per package
MAKTABA_TEST_PG_URL="$PG" go test -tags=integration -count=1 -skip "$skip" $pkgs
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  echo "compat-prev: $PREV runs on this build's database"
  exit 0
fi
if [ -n "$breaking" ]; then
  echo "compat-prev: $PREV fails on this build's database, as the migration(s)$breaking say (\"Breaking for the previous release\": the release is held for an admin)"
  exit 0
fi
echo "compat-prev: $PREV fails on this build's database and no migration newer than it is marked \"Breaking for the previous release\" in shared/db/migrations/MANIFEST.md: an automatic update's rollback would run it there (story 35.5 AC6). Make the migration additive, or mark it." >&2
exit 1
