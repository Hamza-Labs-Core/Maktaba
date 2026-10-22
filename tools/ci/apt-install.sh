#!/bin/sh
# apt-install.sh [APT-GET-INSTALL-OPTION...] PACKAGE... : apt-get update, then install PACKAGE...,
# failing fast on a stalled mirror instead of eating the job's time limit. apt gives up on a
# connection silent for 30 s and tries a file 3 more times; the network part (the update, then
# the download of the packages) is cut off after APT_NET_TIMEOUT seconds (default 300; it
# usually takes under a minute), and run once more APT_RETRY_WAIT seconds later (default 15)
# when it fails. The packages are then installed from the downloaded files, so a cut-off never
# stops dpkg half way. Each phase's time goes to stderr, so a slow mirror shows in the log when
# the caller silences apt. apt runs as root: through sudo on a runner, as is in a container.
set -eu

if [ "$#" -eq 0 ]; then
	echo "usage: $0 [apt-get install option...] package..." >&2
	exit 2
fi
net_timeout=${APT_NET_TIMEOUT:-300}
retry_wait=${APT_RETRY_WAIT:-15}

if [ "$(id -u)" -eq 0 ]; then
	as_root() { "$@"; }
else
	as_root() { sudo "$@"; }
fi

# run_apt LIMIT ARG... : apt-get ARG... as root, quiet, never asking, cut off after LIMIT
# seconds (0: never)
run_apt() {
	limit=$1
	shift
	set -- env DEBIAN_FRONTEND=noninteractive apt-get -qq \
		-o Acquire::Retries=3 \
		-o Acquire::http::Timeout=30 \
		-o Acquire::https::Timeout=30 \
		"$@"
	if [ "$limit" -gt 0 ]; then
		set -- timeout -k 10 "$limit" "$@"
	fi
	as_root "$@"
}

# phase NAME CMD... : CMD, and how long it took
phase() {
	name=$1
	shift
	start=$(date +%s)
	rc=0
	"$@" || rc=$?
	end=$(date +%s)
	case $rc in
	0) echo "apt-install: $name: $((end - start)) s" >&2 ;;
	124 | 137) echo "apt-install: $name: cut off after $((end - start)) s" >&2 ;;
	*) echo "apt-install: $name: failed (exit $rc) after $((end - start)) s" >&2 ;;
	esac
	return "$rc"
}

# fetch PACKAGE-ARG... : the update and the download, within net_timeout seconds together
fetch() {
	deadline=$(($(date +%s) + net_timeout))
	phase update run_apt "$net_timeout" update &&
		phase download run_apt "$(left)" install -y --download-only "$@"
}

left() {
	l=$((deadline - $(date +%s)))
	[ "$l" -gt 0 ] || l=1
	echo "$l"
}

if ! fetch "$@"; then
	echo "::warning::apt-install: apt failed or stalled; trying again in ${retry_wait} s" >&2
	sleep "$retry_wait"
	if ! fetch "$@"; then
		echo "::error::apt-install: apt failed twice ($*)" >&2
		exit 1
	fi
fi
phase install run_apt 0 install -y "$@"
