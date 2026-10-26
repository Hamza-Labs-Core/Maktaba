#!/usr/bin/env bash
# apt-install.sh <package>...: install Ubuntu packages on a CI runner without a stuck
# mirror eating the job. The runner's third-party sources (Microsoft, Azure, ...) are
# not read: every package Maktaba's CI installs comes from Ubuntu's own archive. Each
# update gets 3 minutes, three tries, a 30 s network timeout per request; the install
# APT_INSTALL_TIMEOUT (default 10 minutes), twice, the second from Ubuntu's own archive.
set -euo pipefail
[ $# -gt 0 ] || { echo "usage: apt-install.sh <package>..." >&2; exit 2; }

# sources.list (Ubuntu's own on 22.04) stays; of sources.list.d only Ubuntu's file
# (24.04 keeps its sources there, deb822) is read, from a folder of its own
parts=$(mktemp -d)
trap 'rm -rf "$parts"' EXIT
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  cp /etc/apt/sources.list.d/ubuntu.sources "$parts/"
fi
# a lock another apt holds is waited for a minute at most, not for the whole step
opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30
      -o DPkg::Lock::Timeout=60 -o Dpkg::Progress-Fancy=0 -o Dpkg::Use-Pty=0
      -o "Dir::Etc::sourceparts=$parts" -o APT::Get::List-Cleanup=0)

# APT_CACHE_DIR keeps the downloaded packages where CI's cache saves and restores them:
# the runner's mirror has served 50 KB/s for a whole step, and a warm run fetches nothing
if [ -n "${APT_CACHE_DIR:-}" ]; then
  mkdir -p "$APT_CACHE_DIR/partial"
  opts+=(-o "Dir::Cache::Archives=$APT_CACHE_DIR")
fi

for try in 1 2 3; do
  if sudo timeout 180 apt-get "${opts[@]}" update -qq; then
    break
  fi
  [ "$try" = 3 ] && { echo "::error::apt-get update failed three times" >&2; exit 1; }
  echo "::warning::apt-get update failed (try $try), trying again" >&2
  sleep 10
done
# Ubuntu 22.04's needrestart asks, after an install, which services to restart: with no
# terminal to answer it the install never ends. It restarts nothing here.
export NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1
# shellcheck disable=SC2016 # needrestart's own Perl, kept as it is
echo '$nrconf{restart} = "a";' | sudo tee /etc/needrestart/conf.d/90-ci.conf >/dev/null 2>&1 || true

# man-db rebuilding its index after every install is the known stall of GitHub's
# runners (minutes for ffmpeg's hundred packages): CI reads no man pages
echo "man-db man-db/auto-update boolean false" | sudo debconf-set-selections
sudo rm -f /var/lib/man-db/auto-update

# the runner's mirror (azure.archive.ubuntu.com) has stalled a download for the whole
# step: an install gets APT_INSTALL_TIMEOUT seconds (10 minutes; a big set such as the
# Linux app's WebKit and GTK needs several on a good day), and a second try reads
# Ubuntu's own archive
limit=${APT_INSTALL_TIMEOUT:-600}
for try in 1 2; do
  # -q, not -qq: a stall shows the package it stopped on
  if sudo DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1 timeout "$limit" \
    apt-get "${opts[@]}" install -y -q --no-install-recommends "$@" >&2; then
    # the cache's files are root's after apt: the cache action saves them as the runner
    [ -n "${APT_CACHE_DIR:-}" ] && sudo chown -R "$(id -u):$(id -g)" "$APT_CACHE_DIR"
    exit 0
  fi
  [ "$try" = 2 ] && { echo "::error::apt-get install failed twice" >&2; exit 1; }
  echo "::warning::apt-get install failed or stalled (try $try), trying Ubuntu's own archive" >&2
  # the runner reads its mirrors from /etc/apt/apt-mirrors.txt (sources say
  # mirror+file:...): Ubuntu's own archive goes first there, and in plain sources
  if [ -f /etc/apt/apt-mirrors.txt ]; then
    printf 'http://archive.ubuntu.com/ubuntu/\tpriority:1\n' | sudo tee /etc/apt/apt-mirrors.txt >/dev/null
  fi
  for f in /etc/apt/sources.list "$parts"/*.sources; do
    [ -f "$f" ] && sudo sed -i 's#http://azure\.archive\.ubuntu\.com#http://archive.ubuntu.com#g' "$f"
  done
  sudo timeout 180 apt-get "${opts[@]}" update -qq || true
done
