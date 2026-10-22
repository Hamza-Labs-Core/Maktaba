#!/bin/sh
# apt-install.sh [APT-GET-INSTALL-OPTION...] PACKAGE... : apt-get update, then install PACKAGE...,
# failing fast on a stalled mirror instead of eating the job's time limit. apt gives up on a
# connection silent for 30 s and tries a file 3 more times; the network part (the update, then
# the download of the packages) is cut off after APT_NET_TIMEOUT seconds (default 300; it
# usually takes under a minute). The packages are then installed from the downloaded files, so
# a cut-off never stops dpkg half way. When the first try fails, the second, APT_RETRY_WAIT
# seconds later (default 15), is the plain install (the download and the install in one
# apt-get, after an update within the same time). Each phase's time goes to stderr; apt's own
# output goes to a file whose tail is shown, with apt's processes, when a phase fails. apt runs
# as root: through sudo on a runner, as is in a container.
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

# apt's output goes to this file, never to a terminal: on the x86_64 runners sudo gives apt-get
# one, and under timeout (in a process group of its own) the first line apt-get wrote to it
# stopped it until the cut-off. timeout --foreground keeps it in the step's group as well.
log=$(mktemp)
trap 'rm -f "$log"' EXIT

# run_apt LIMIT ARG... : apt-get ARG... as root, never asking, its output to $log, cut off
# after LIMIT seconds (0: never). It waits up to 5 minutes for a dpkg lock another apt holds
# (a runner's own updates at boot).
run_apt() {
	limit=$1
	shift
	set -- env DEBIAN_FRONTEND=noninteractive apt-get -q \
		-o Acquire::Retries=3 \
		-o Acquire::http::Timeout=30 \
		-o Acquire::https::Timeout=30 \
		-o DPkg::Lock::Timeout=300 \
		"$@"
	if [ "$limit" -gt 0 ]; then
		set -- timeout --foreground -k 10 "$limit" "$@"
	fi
	as_root "$@" </dev/null >>"$log" 2>&1
}

# tell_failure: what apt was doing (its processes; a stopped one shows T) and its last lines
tell_failure() {
	echo "apt-install: apt and dpkg processes:" >&2
	ps -eo pid,stat,etime,args 2>/dev/null | grep -E '[a]pt|[d]pkg|[u]nattended' >&2 || true
	echo "apt-install: apt's last lines:" >&2
	tail -n 30 "$log" >&2 || true
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
	124 | 137)
		echo "apt-install: $name: cut off after $((end - start)) s" >&2
		tell_failure
		;;
	*)
		echo "apt-install: $name: failed (exit $rc) after $((end - start)) s" >&2
		tell_failure
		;;
	esac
	return "$rc"
}

left() {
	l=$((deadline - $(date +%s)))
	[ "$l" -gt 0 ] || l=1
	echo "$l"
}

# the first try: the update and the download within net_timeout seconds together, then the
# install from the downloaded files
deadline=$(($(date +%s) + net_timeout))
if phase update run_apt "$net_timeout" update &&
	phase download run_apt "$(left)" install -y --download-only "$@"; then
	phase install run_apt 0 install -y "$@"
	exit
fi

# the second: the plain install, the download and the install in one apt-get, bounded only by
# the step's time limit (as the steps were before this script), so dpkg is never stopped half way
echo "::warning::apt-install: apt failed or stalled; trying a plain install in ${retry_wait} s" >&2
sleep "$retry_wait"
if ! { phase update run_apt "$net_timeout" update &&
	phase install run_apt 0 install -y "$@"; }; then
	echo "::error::apt-install: apt failed twice ($*)" >&2
	exit 1
fi
