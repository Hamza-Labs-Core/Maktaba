#!/usr/bin/env bash
# Records GitHub's first page of this repository's releases exactly as a home reads it
# (api/internal/updates/catalog.go: listPage): unauthenticated, Accept
# application/vnd.github+json, X-GitHub-Api-Version 2022-11-28, Go's User-Agent,
# per_page=10. With those headers GitHub answers minified JSON; with curl's default
# Accept (*/*) it pretty-prints the same list, some 23% longer, which is not what a home
# reads. Unauthenticated, because an authenticated list from an account with push access
# puts the draft releases first.
#
#   tests/fixtures/github-releases/record.sh [REPO]
#
# Writes page-1.json.gz beside this script and prints its size. Re-record when the shape
# of a release changes (many more assets a release, longer notes); the tests that read it
# (api/internal/updates/catalog_feed_test.go and the sweep's, docs/updates.md "The
# release feed's caps") then check every cap against it.
set -euo pipefail
repo="${1:-Hamza-Labs-Core/Maktaba}"
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT
curl -fsS -A 'Go-http-client/1.1' \
    -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' \
    -o "${tmp}" "https://api.github.com/repos/${repo}/releases?per_page=10&page=1"
gzip -9 -n -c "${tmp}" >"${here}/page-1.json.gz"
echo "page-1.json.gz: $(wc -c <"${tmp}") bytes as GitHub answered, $(wc -c <"${here}/page-1.json.gz") gzipped"
