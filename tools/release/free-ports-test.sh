#!/usr/bin/env bash
# Checks tools/release/free-ports.sh: the ports it gives are below every ephemeral range, and a
# run with a port something listens on is skipped. Needs bash and python3 (the listener).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=tools/release/free-ports.sh
. "${here}/free-ports.sh"
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

for _ in $(seq 1 20); do
    p="$(free_ports 4)"
    if [ "$p" -lt 20000 ] || [ $((p + 3)) -gt 29999 ]; then fail "free_ports 4 gave ${p}: outside 20000-29999"; fi
done
[ "$fails" -eq 0 ] && ok "20 runs of 4 ports, each inside 20000-29999"

# a run with a port something listens on is skipped: start at a base whose third port is busy
first=24000
busy=$((first + 2))
python3 -c 'import socket,sys,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(); time.sleep(30)' "$busy" &
lpid=$!
for _ in $(seq 1 50); do (exec 3<>"/dev/tcp/127.0.0.1/${busy}") 2>/dev/null && break; sleep 0.1; done
idle="$(FREE_PORTS_FROM=$((first + 10)) free_ports 4)"
got="$(FREE_PORTS_FROM=${first} free_ports 4)"
kill "$lpid" 2>/dev/null || true
wait "$lpid" 2>/dev/null || true
if [ "$idle" = $((first + 10)) ]; then ok "a free run is taken as it is (${idle})"; else fail "a free run at $((first + 10)) gave ${idle}"; fi
if [ "$got" = $((first + 4)) ]; then
    ok "a run with a listener on ${busy} is skipped (${first} -> ${got})"
else
    fail "with ${busy} listening, free_ports 4 from ${first} gave ${got}, want $((first + 4))"
fi

[ "$fails" -eq 0 ] || { echo "${fails} failure(s)"; exit 1; }
