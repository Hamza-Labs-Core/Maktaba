#!/usr/bin/env bash
# The Sigstore trusted root compiled into releaseverify (shared/go/releaseverify/
# trusted_root.json) has to be fresh: a release build refuses one older than 90 days
# (releaseverify's TestEmbeddedRootAge, MAKTABA_RELEASE_BUILD=1), so the root embedded on
# 2026-10-19 would fail every release from 2027-01-17. The scheduled trusted-root.yml
# refreshes it and opens a pull request; this check, in CI on every change, warns 30 days
# before that limit and fails at it.
#
# Usage: trusted-root-age.sh [META]   (default: shared/go/releaseverify/trusted_root.meta.json)
# Env: NOW (an epoch, for tests), LIMIT_DAYS (90), WARN_DAYS (30).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
meta="${1:-${here}/../../shared/go/releaseverify/trusted_root.meta.json}"
limit="${LIMIT_DAYS:-90}"
warn="${WARN_DAYS:-30}"

fetched="$(tr -d '\r' <"${meta}" | sed -n 's/.*"fetched_at": *"\([^"]*\)".*/\1/p')"
[ -n "${fetched}" ] || { echo "::error::${meta}: no fetched_at"; exit 1; }
then_s="$(date -u -d "${fetched}" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "${fetched}" +%s)"
now_s="${NOW:-$(date -u +%s)}"
age=$(( (now_s - then_s) / 86400 ))
left=$(( limit - age ))

if [ "${left}" -le 0 ]; then
    echo "::error::the embedded Sigstore trusted root is ${age} days old (fetched ${fetched}): release builds refuse it past ${limit} days. Merge trusted-root.yml's pull request, or run \`go generate ./...\` in shared/go/releaseverify and commit"
    exit 1
fi
if [ "${left}" -le "${warn}" ]; then
    echo "::warning::the embedded Sigstore trusted root is ${age} days old (fetched ${fetched}): release builds refuse it in ${left} days. Merge trusted-root.yml's pull request, or run \`go generate ./...\` in shared/go/releaseverify and commit"
    exit 0
fi
echo "the embedded Sigstore trusted root is ${age} days old (fetched ${fetched}); release builds take it for ${left} more days"
