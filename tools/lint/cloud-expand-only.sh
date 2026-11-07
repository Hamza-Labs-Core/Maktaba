#!/usr/bin/env bash
# Cloud migrations expand only (Epic 50 story 50.08 §2): a release's cloud migration may add
# (a table, a nullable column, an index CONCURRENTLY) but not break the release still running
# beside it during a blue/green deploy. A migration that drops, renames, changes a type or
# sets NOT NULL is a *contract* migration: it ships in a later release, once nothing uses the
# old shape, and its file's first line says so:
#     -- contract: <what the previous release no longer uses>
#
# Usage: cloud-expand-only.sh [BASE]   the cloud migrations added since BASE (default: the merge
#                                      base with origin/main)
#        cloud-expand-only.sh --files <file>...   check those files
set -uo pipefail

check() { # check <file>: 0 when it expands only or is marked a contract
    local f=$1 up first=""
    IFS= read -r first <"$f" || true
    shopt -s nocasematch
    if [[ "${first}" =~ ^--[[:space:]]*contract: ]]; then shopt -u nocasematch; return 0; fi
    shopt -u nocasematch
    # only the Up part counts (the Down part undoes, and may drop)
    up="$(awk '/^-- \+goose Down/{exit} {print}' "$f" | sed 's/--.*$//')"
    local bad
    bad="$(grep -niE '\b(DROP[[:space:]]+(TABLE|COLUMN|INDEX|CONSTRAINT|VIEW|TYPE|SCHEMA|FUNCTION|TRIGGER)|RENAME[[:space:]]+(TO|COLUMN|CONSTRAINT)|ALTER[[:space:]]+COLUMN[[:space:]]+[a-z_"]+[[:space:]]+(SET[[:space:]]+DATA[[:space:]]+)?TYPE|SET[[:space:]]+NOT[[:space:]]+NULL)\b' <<<"${up}" \
        | grep -viE 'DROP[[:space:]]+(TRIGGER|FUNCTION)[[:space:]]+IF[[:space:]]+EXISTS' || true)"
    if [ -n "${bad}" ]; then
        echo "${f}: a contract change in a release's migration (blue/green runs the previous release on this schema):"
        while IFS= read -r l; do echo "    ${l}"; done <<<"${bad}"
        echo "    split it: expand now, contract in a later release with a first line \"-- contract: ...\""
        return 1
    fi
    return 0
}

files=()
if [ "${1:-}" = --files ]; then
    shift; files=("$@")
else
    base="${1:-$(git merge-base origin/main HEAD)}"
    while IFS= read -r f; do files+=("$f"); done < <(git diff --name-only --diff-filter=A "${base}" HEAD -- 'cloud/migrations/*.sql')
fi
rc=0
for f in "${files[@]}"; do check "$f" || rc=1; done
exit "${rc}"
