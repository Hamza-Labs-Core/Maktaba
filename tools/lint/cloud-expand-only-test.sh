#!/usr/bin/env bash
# Tests for cloud-expand-only.sh (story 50.08 §2).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; trap 'rm -rf "${work}"' EXIT
fails=0
expect() { # expect pass|fail <name> <sql>
    printf '%s\n' "$3" >"${work}/$2.sql"
    if bash "${here}/cloud-expand-only.sh" --files "${work}/$2.sql" >"${work}/$2.out" 2>&1; then got=pass; else got=fail; fi
    if [ "${got}" = "$1" ]; then echo "ok   $1 $2"; else echo "FAIL $2: want $1, got ${got}: $(cat "${work}/$2.out")"; fails=$((fails + 1)); fi
}
expect pass add-table $'-- +goose Up\nCREATE TABLE t (id int);\n-- +goose Down\nDROP TABLE t;'
expect pass add-column $'-- +goose Up\nALTER TABLE t ADD COLUMN c text;\n-- +goose Down\nALTER TABLE t DROP COLUMN c;'
expect pass trigger-replace $'-- +goose Up\nDROP TRIGGER IF EXISTS x ON t;\nCREATE TRIGGER x AFTER UPDATE ON t FOR EACH ROW EXECUTE FUNCTION f();'
expect fail drop-column $'-- +goose Up\nALTER TABLE t DROP COLUMN c;'
expect fail rename $'-- +goose Up\nALTER TABLE t RENAME COLUMN a TO b;'
expect fail type $'-- +goose Up\nALTER TABLE t ALTER COLUMN a TYPE bigint;'
expect fail not-null $'-- +goose Up\nALTER TABLE t ALTER COLUMN a SET NOT NULL;'
expect pass contract $'-- contract: the previous release no longer reads t.c\n-- +goose Up\nALTER TABLE t DROP COLUMN c;'
expect pass in-a-comment $'-- +goose Up\n-- we never DROP TABLE here\nCREATE INDEX CONCURRENTLY i ON t (c);'
# no new migrations: an empty list passes, also under macOS's bash 3.2, where "${files[@]}" on an
# empty array is an unbound variable under set -u
if bash "${here}/cloud-expand-only.sh" --files >/dev/null 2>"${work}/empty.err"; then echo "ok   no files"; else echo "FAIL no files: $(cat "${work}/empty.err")"; fails=$((fails + 1)); fi
# every cloud migration in the repository today expands only (or is older than the rule)
if bash "${here}/cloud-expand-only.sh" --files "${here}"/../../cloud/migrations/0050*.sql >/dev/null; then echo "ok   the repository's Epic 50 migrations"; else echo "FAIL the repository's Epic 50 migrations"; fails=$((fails + 1)); fi
if [ "${fails}" -ne 0 ]; then echo "${fails} failed"; exit 1; fi
echo "all passed"
