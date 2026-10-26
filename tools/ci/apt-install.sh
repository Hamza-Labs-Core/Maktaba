#!/usr/bin/env bash
# apt-install.sh <package>...: install Ubuntu packages on a CI runner without a stuck
# mirror eating the job. The runner's third-party sources (Microsoft, Azure, ...) are
# not read: every package Maktaba's CI installs comes from Ubuntu's own archive. Each
# update gets 3 minutes, three tries, a 30 s network timeout per request.
set -euo pipefail
[ $# -gt 0 ] || { echo "usage: apt-install.sh <package>..." >&2; exit 2; }

# sources.list (Ubuntu's own on 22.04) stays; of sources.list.d only Ubuntu's file
# (24.04 keeps its sources there, deb822) is read, from a folder of its own
parts=$(mktemp -d)
trap 'rm -rf "$parts"' EXIT
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  cp /etc/apt/sources.list.d/ubuntu.sources "$parts/"
fi
opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30
      -o "Dir::Etc::sourceparts=$parts" -o APT::Get::List-Cleanup=0)

for try in 1 2 3; do
  if sudo timeout 180 apt-get "${opts[@]}" update -qq; then
    break
  fi
  [ "$try" = 3 ] && { echo "::error::apt-get update failed three times" >&2; exit 1; }
  echo "::warning::apt-get update failed (try $try), trying again"
  sleep 10
done
sudo DEBIAN_FRONTEND=noninteractive timeout 600 apt-get "${opts[@]}" install -y -qq --no-install-recommends "$@"
