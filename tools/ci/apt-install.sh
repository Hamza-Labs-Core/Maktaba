#!/bin/sh
# apt-install.sh [APT-GET-INSTALL-OPTION...] PACKAGE... : apt-get update, then install
# PACKAGE..., tried once more APT_RETRY_WAIT seconds later (default 15) when it fails. apt
# runs as the steps ran it before this script, in plain sight of the step's log (no timeout
# around it, no redirection: on the x86_64 runners apt under sudo with either stopped dead
# until the step's limit); a mirror that stalls is cut off by the step's timeout-minutes.
# Each phase's time goes to stderr. apt runs as root: through sudo on a runner, as is in a
# container.
set -eu

if [ "$#" -eq 0 ]; then
	echo "usage: $0 [apt-get install option...] package..." >&2
	exit 2
fi
retry_wait=${APT_RETRY_WAIT:-15}

if [ "$(id -u)" -eq 0 ]; then
	as_root() { "$@"; }
else
	as_root() { sudo "$@"; }
fi

# run_apt ARG... : apt-get ARG... as root, never asking; it waits up to 5 minutes for a dpkg
# lock another apt holds (a runner's own updates at boot)
run_apt() {
	as_root env DEBIAN_FRONTEND=noninteractive apt-get \
		-o Acquire::Retries=3 \
		-o DPkg::Lock::Timeout=300 \
		"$@"
}

# phase NAME CMD... : CMD, and how long it took
phase() {
	name=$1
	shift
	start=$(date +%s)
	rc=0
	"$@" || rc=$?
	end=$(date +%s)
	if [ "$rc" -eq 0 ]; then
		echo "apt-install: $name: $((end - start)) s" >&2
	else
		echo "apt-install: $name: failed (exit $rc) after $((end - start)) s" >&2
	fi
	return "$rc"
}

attempt() {
	phase update run_apt update && phase install run_apt install -y "$@"
}

if ! attempt "$@"; then
	echo "::warning::apt-install: apt failed; trying again in ${retry_wait} s" >&2
	sleep "$retry_wait"
	if ! attempt "$@"; then
		echo "::error::apt-install: apt failed twice ($*)" >&2
		exit 1
	fi
fi
