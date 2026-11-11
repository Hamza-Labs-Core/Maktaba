#!/usr/bin/env bash
# Story 60.08 T08: image-public.sh's positive and negative controls.
#   positive: a public multi-architecture ghcr.io image passes (token and amd64 + arm64 pulls);
#   negative: an image an anonymous client may not read fails, with the error the release run
#   shows. The negative control is a repository under the organization that does not exist:
#   ghcr.io refuses an anonymous token for it as it does for a private one, and it stays
#   refused once the server image is public. NO_PULL=1 runs the token checks alone.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
log="$(mktemp)"
trap 'rm -f "${log}"' EXIT
export ATTEMPTS=1 WAIT=0

echo "== positive control: ghcr.io/linuxserver/baseimage-alpine:3.20"
bash "${here}/image-public.sh" ghcr.io/linuxserver/baseimage-alpine 3.20

echo "== negative control: ghcr.io/hamza-labs-core/maktaba-image-public-negative-control:latest"
if bash "${here}/image-public.sh" ghcr.io/hamza-labs-core/maktaba-image-public-negative-control latest >"${log}" 2>&1; then
  cat "${log}"
  echo "FAIL: the negative control passed"
  exit 1
fi
cat "${log}"
grep -q '^::error::the server image is not pullable without signing in$' "${log}" ||
  { echo "FAIL: the negative control did not print the error"; exit 1; }
grep -q 'story-60-08-link-integrity.md' "${log}" ||
  { echo "FAIL: the error does not name the story"; exit 1; }
echo "ok: image-public.sh passes a public image and fails one it cannot read"
