#!/usr/bin/env bash
# golangci-lint for the lint jobs (_lint.yml's lint, relay.yml's lint): its pinned release,
# fetched by tools/release/fetch-pinned.sh (the GitHub API's route first, then github.com,
# 5 tries with backoff, the SHA-256 checked) and put on the job's PATH. The jobs run
# golangci-lint-action with `install-mode: none`, which runs this binary.
#
#   golangci-lint.sh --key   the actions/cache key for the release archive
#   golangci-lint.sh DIR     DIR holds the archive (kept when it has the pin: a restored
#                            cache downloads nothing), the binary goes in DIR/bin, and
#                            DIR/bin is added to $GITHUB_PATH when Actions runs it
#
# The action's own download of golangci-lint (github.com's release URL, no retry) failed
# main's lint with a 504 on 2026-11-08. Linux x86_64 only: both jobs run on ubuntu.
# tools/ci/tests/test_golangci_lint.py checks the workflows run it and pin nothing else.
set -euo pipefail

VERSION=2.12.2
# golangci-lint-2.12.2-checksums.txt, from the release
SHA256=8df580d2670fed8fa984aac0507099af8df275e665215f5c7a2ae3943893a553
NAME="golangci-lint-$VERSION-linux-amd64"
URL="https://github.com/golangci/golangci-lint/releases/download/v$VERSION/$NAME.tar.gz"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fetch="$here/../release/fetch-pinned.sh"

if [ "${1:-}" = --key ]; then
    "$fetch" --key "$URL" "$SHA256"
    exit 0
fi
[ $# -eq 1 ] || { echo "usage: golangci-lint.sh --key | DIR" >&2; exit 2; }
case "$(uname -s)/$(uname -m)" in
    Linux/x86_64) ;;
    *) echo "::error::golangci-lint.sh pins the linux-amd64 build; this is $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac

dir="$1"
tgz="$("$fetch" "$URL" "$SHA256" "$dir/$NAME.tar.gz")"
rm -rf "${dir:?}/bin"
mkdir -p "$dir/bin"
tar -xzf "$tgz" -C "$dir/bin" --strip-components=1 "$NAME/golangci-lint"
got="$("$dir/bin/golangci-lint" version 2>&1)"
case "$got" in
    *"$VERSION"*) printf 'golangci-lint: %s\n' "$got" ;;
    *) echo "::error::$dir/bin/golangci-lint is not $VERSION: $got" >&2; exit 1 ;;
esac
if [ -n "${GITHUB_PATH:-}" ]; then
    (cd "$dir/bin" && pwd) >>"$GITHUB_PATH"
fi
