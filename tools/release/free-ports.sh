# shellcheck shell=bash
# Sourced by the app checks (mac-app, linux-app, pg18-check.sh): free_ports N prints the first of
# N consecutive TCP ports on 127.0.0.1 that nothing listens on, for the scratch servers they
# start.
#
# The ports come from 20000-29999: below every system's ephemeral range (Linux from 32768,
# macOS and Windows from 49152), where an outgoing connection's own end (a psql session, a
# download) can hold a port a server then cannot bind. pg18-check.sh once drew its ports from
# 55000-55899 and a PostgreSQL 16 server failed "could not create listen socket for
# 127.0.0.1" on a macOS runner. Each port is also probed (bash's /dev/tcp: a connection that
# succeeds means something listens), and a busy run is skipped. FREE_PORTS_FROM=P starts at P
# and moves up past a busy run instead of drawing at random (the test's way to be exact).

free_ports() {
    local n="${1:-1}" base i busy _
    base="${FREE_PORTS_FROM:-}"
    for _ in $(seq 1 50); do
        if [ -z "${FREE_PORTS_FROM:-}" ]; then base=$((20000 + RANDOM % (10000 - n))); fi
        busy=""
        for ((i = 0; i < n; i++)); do
            if (exec 3<>"/dev/tcp/127.0.0.1/$((base + i))") 2>/dev/null; then
                busy=1
                break
            fi
        done
        [ -z "$busy" ] && { printf '%s\n' "$base"; return 0; }
        base=$((base + n))
    done
    printf '%s\n' "$base"
}
