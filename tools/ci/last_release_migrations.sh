#!/usr/bin/env bash
# The last release's cloud migrations, for the cloud's integration tests (TestUpFromTheLastRelease):
# a database an update migrates is never a new one, and a new one hides what a late lower slot breaks.
#
#   tools/ci/last_release_migrations.sh DIR     writes DIR/cloud/migrations, and
#                                               MAKTABA_LAST_RELEASE_MIGRATIONS into $GITHUB_ENV
#
# On a CI runner only, and through a repository of its own: a fetch that is not deep makes the clone it
# is run in, and every worktree that shares it, shallow.
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] || { echo "last_release_migrations.sh: on a CI runner only" >&2; exit 1; }
dir="$1"
url="$(git remote get-url origin)"
tries() { # tries CMD...: three times, the network being what it is
	local n
	for n in 1 2 3; do
		if "$@"; then return 0; fi
		[ "$n" = 3 ] || sleep $((n * 5))
	done
	return 1
}
tags="$(tries git ls-remote --tags --refs "$url" 'v*')"
versions="$(sed -n 's#.*refs/tags/\(v[0-9]*\.[0-9]*\.[0-9]*\)$#\1#p' <<<"$tags" | sort -V)"
if [ "${GITHUB_REF_TYPE:-}" = tag ]; then
	# a tag's own run: the release before it, not itself
	versions="$(awk -v t="${GITHUB_REF_NAME:-}" '$0 != t' <<<"$versions")"
fi
tag="$(sed -n '$p' <<<"$versions")"
[ -n "$tag" ] || { echo "no release tag" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
git init -q "$tmp"
tries git -C "$tmp" fetch -q --depth 1 --no-tags "$url" "refs/tags/$tag"
mkdir -p "$dir"
git -C "$tmp" archive FETCH_HEAD cloud/migrations | tar -x -C "$dir"
echo "the cloud's migrations of $tag: $(find "$dir/cloud/migrations" -name '*.sql' | wc -l | tr -d ' ')"
[ -z "${GITHUB_ENV:-}" ] || echo "MAKTABA_LAST_RELEASE_MIGRATIONS=$dir/cloud/migrations" >>"$GITHUB_ENV"
