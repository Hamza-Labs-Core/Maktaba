#!/usr/bin/env bash
# The server image is pullable without signing in (Epic 60, story 60.08).
#
# Every install guide pulls the server image; a household has no registry login. This pulls
# it the way a household does: no `docker login`, DOCKER_CONFIG an empty folder, so no stored
# credential or helper is used.
#   1. the registry's anonymous token request for the repository must answer a token;
#   2. `docker pull --platform <p> IMAGE:TAG` must pass for each platform and each tag.
# It tries ATTEMPTS times (3), WAIT seconds apart (60: three tries over two minutes, for the
# registry's own outages), then fails with the error below. A failed check unpublishes
# nothing; it only makes the failure visible.
#
# Usage: image-public.sh IMAGE TAG [TAG...]
#   IMAGE      e.g. ghcr.io/hamza-labs-core/maktaba-server
#   PLATFORMS  "linux/amd64 linux/arm64" (default)
#   ATTEMPTS, WAIT  the retries (3, 60)
#   NO_PULL=1  the token check alone (no Docker)
set -uo pipefail

IMAGE="${1:?usage: image-public.sh IMAGE TAG [TAG...]}"
shift
[ "$#" -ge 1 ] || { echo "usage: image-public.sh IMAGE TAG [TAG...]" >&2; exit 2; }
TAGS=("$@")
read -r -a PLATFORMS <<<"${PLATFORMS:-linux/amd64 linux/arm64}"
ATTEMPTS="${ATTEMPTS:-3}"
WAIT="${WAIT:-60}"

registry="${IMAGE%%/*}"
repo="${IMAGE#*/}"

# one try: 0 when the token and every pull pass
try() {
  local body tag platform out
  body="$(curl -sS -m 30 "https://${registry}/token?scope=repository:${repo}:pull" 2>&1)" || true
  if ! printf '%s' "${body}" | jq -e '(.token // .access_token // "") | length > 0' >/dev/null 2>&1; then
    echo "the anonymous token request for ${repo} answered: ${body}"
    return 1
  fi
  echo "the anonymous token request for ${repo} answers a token"
  [ "${NO_PULL:-}" = 1 ] && return 0
  for tag in "${TAGS[@]}"; do
    for platform in "${PLATFORMS[@]}"; do
      if ! out="$(DOCKER_CONFIG="${empty}" docker pull --quiet --platform "${platform}" "${IMAGE}:${tag}" 2>&1)"; then
        echo "docker pull --platform ${platform} ${IMAGE}:${tag} with no login: ${out}"
        return 1
      fi
      echo "pulled ${IMAGE}:${tag} (${platform}) with no login"
      DOCKER_CONFIG="${empty}" docker image rm "${IMAGE}:${tag}" >/dev/null 2>&1 || true
    done
  done
  return 0
}

empty="$(mktemp -d)"
trap 'rm -rf "${empty}"' EXIT
for ((i = 1; i <= ATTEMPTS; i++)); do
  if try; then
    echo "${IMAGE} (${TAGS[*]}) is pullable without signing in"
    exit 0
  fi
  if [ "${i}" -lt "${ATTEMPTS}" ]; then
    echo "try ${i} of ${ATTEMPTS} failed; again in ${WAIT}s"
    sleep "${WAIT}"
  fi
done
echo "::error::the server image is not pullable without signing in"
echo "::error::${IMAGE} (${TAGS[*]}) refused an anonymous pull. Make the package public: the package's settings on GitHub (Package settings, Danger Zone, Change visibility, Public). See specs/epics/60-website/story-60-08-link-integrity.md (owner action A1)."
exit 1
