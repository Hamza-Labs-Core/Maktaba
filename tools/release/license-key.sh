#!/usr/bin/env bash
# Embeds the license-server public key in the home's API (Epic 16 story
# 16.4: "the server bundles the license-server public key at build
# time"). The tree holds a placeholder (api/internal/subscriptions/keys/
# license-server.pub.pem): a build that keeps it cannot apply any license
# and every home it makes stays on the free tier. A release writes the
# real key over it before compiling anything.
#
# The key is the public half of Maktaba Cloud's entitlement key (the
# cloud signs plan licenses with it; `maktaba-cloud license pubkey`
# prints it). It is not a secret; it lives in the GitHub Actions variable
# MAKTABA_LICENSE_PUBKEY.
#
#   MAKTABA_LICENSE_PUBKEY="$(cat key.pem)" tools/release/license-key.sh [--require]
#
# --require fails when the variable is unset (a release must not ship
# without it). Without --require an unset variable leaves the placeholder.
set -euo pipefail

require=0
[ "${1:-}" = "--require" ] && require=1
dest="$(cd "$(dirname "$0")/../.." && pwd)/api/internal/subscriptions/keys/license-server.pub.pem"

key="${MAKTABA_LICENSE_PUBKEY:-}"
if [ -z "$key" ]; then
  if [ "$require" = 1 ]; then
    echo "license-key: MAKTABA_LICENSE_PUBKEY is not set; a release must embed the license-server key" >&2
    exit 1
  fi
  echo "license-key: MAKTABA_LICENSE_PUBKEY not set; the key file is left as it is" >&2
  exit 0
fi
# one PEM PUBLIC KEY block and nothing else
lines="$(printf '%s\n' "$key" | sed '/^[[:space:]]*$/d')"
first="$(printf '%s\n' "$lines" | head -n 1)"
last="$(printf '%s\n' "$lines" | tail -n 1)"
if [ "$first" != "-----BEGIN PUBLIC KEY-----" ] || [ "$last" != "-----END PUBLIC KEY-----" ]; then
  echo "license-key: MAKTABA_LICENSE_PUBKEY is not one PEM PUBLIC KEY block" >&2
  exit 1
fi
printf '%s\n' "$lines" > "$dest"
echo "license-key: embedded the license-server key ($(printf '%s\n' "$lines" | sed -n 2p | cut -c1-24)…)" >&2
