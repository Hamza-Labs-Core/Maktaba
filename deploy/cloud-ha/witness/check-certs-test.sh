#!/usr/bin/env bash
# Tests for check-certs.sh (story 50.02 T05): a wildcard with 60 days left passes; one with
# 20 days left fails naming the node and its date; a certificate without the wildcard fails;
# a node that answers no TLS fails as tls_missing. Needs openssl; touches only a temp dir.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null; done; rm -rf "${work}"; }
trap cleanup EXIT
fails=0

mkcert() { # mkcert <name> <days> <san>
    openssl req -x509 -newkey rsa:2048 -nodes -keyout "${work}/$1.key" -out "${work}/$1.pem" \
        -days "$2" -subj "/CN=relay.test" -addext "subjectAltName=$3" >/dev/null 2>&1
}
serve() { # serve <name> <port>
    openssl s_server -quiet -accept "$2" -cert "${work}/$1.pem" -key "${work}/$1.key" -www >/dev/null 2>&1 &
    pids+=($!)
}
mkcert good 60 "DNS:relay.test,DNS:*.relay.test"
mkcert soon 20 "DNS:relay.test,DNS:*.relay.test"
mkcert narrow 60 "DNS:relay.test"
base=$((20000 + RANDOM % 20000))
serve good $base; serve soon $((base + 1)); serve narrow $((base + 2))
for _ in $(seq 1 50); do
    openssl s_client -connect "127.0.0.1:$((base + 2))" </dev/null >/dev/null 2>&1 && break
    sleep 0.1
done

expect() { # expect <0|1> <pattern> <args...>
    local want=$1 pat=$2; shift 2
    out="$(SNI=x.relay.test bash "${here}/check-certs.sh" "$@" 2>&1)"; rc=$?
    if [ "${rc}" = "${want}" ] && grep -qE "${pat}" <<<"${out}"; then
        echo "ok   $*"
    else
        echo "FAIL $*: rc=${rc} out=${out}"; fails=$((fails + 1))
    fi
}
expect 0 "cert a .*: ok, \*\.relay\.test" "a=127.0.0.1:${base}"
expect 1 "cert b .*: expires .*20[0-9][0-9].*, under 21 days" "a=127.0.0.1:${base}" "b=127.0.0.1:$((base + 1))"
expect 1 "cert c .*does not cover \*\.relay\.test" "c=127.0.0.1:$((base + 2))"
expect 1 "cert d .*tls_missing" "d=127.0.0.1:1"

if [ "${fails}" -ne 0 ]; then echo "${fails} failed"; exit 1; fi
echo "all passed"
