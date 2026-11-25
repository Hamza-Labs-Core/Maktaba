#!/usr/bin/env bash
# The last release's cloud migrations, for the cloud's integration tests (TestUpFromTheLastRelease):
# a database an update migrates is never a new one, and a new one hides what a late lower slot breaks.
#
#   tools/ci/last_release_migrations.sh DIR     writes DIR/cloud/migrations and, on a runner,
#                                               MAKTABA_LAST_RELEASE_MIGRATIONS into $GITHUB_ENV
set -euo pipefail
dir="$1"
tags="$(git ls-remote --tags --refs origin 'v*')"
tag="$(sed -n 's#.*refs/tags/\(v[0-9]*\.[0-9]*\.[0-9]*\)$#\1#p' <<<"$tags" | sort -V | sed -n '$p')"
[ -n "$tag" ] || { echo "no release tag" >&2; exit 1; }
git fetch -q --depth 1 --no-tags origin "refs/tags/$tag"
mkdir -p "$dir"
git archive FETCH_HEAD cloud/migrations | tar -x -C "$dir"
echo "the cloud's migrations of $tag: $(find "$dir/cloud/migrations" -name '*.sql' | wc -l | tr -d ' ')"
[ -z "${GITHUB_ENV:-}" ] || echo "MAKTABA_LAST_RELEASE_MIGRATIONS=$dir/cloud/migrations" >>"$GITHUB_ENV"
