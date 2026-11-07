#!/usr/bin/env bash
# The witness's daily certificate check (Epic 50 story 50.02): each node's own wildcard
# certificate, fetched from the node's private address with a home's name as SNI, must have
# more than WARN_DAYS (21) left and cover the wildcard. Exits 1, naming each node and its
# expiry date, when one does not; 50.10's alert pages on that.
#
# Usage: check-certs.sh <name>=<host:port> ...   e.g. a=10.0.1.2:443 b=10.0.1.3:443
# Env: SNI (default x.relay.maktaba.tv), WARN_DAYS (21), CA_FILE (verify against it; default:
# no verification, the expiry and the names are what is checked).
set -uo pipefail

sni="${SNI:-x.relay.maktaba.tv}"
warn_days="${WARN_DAYS:-21}"
[ $# -gt 0 ] || { echo "usage: $0 <name>=<host:port> ..." >&2; exit 2; }

bad=0
for arg in "$@"; do
    node="${arg%%=*}" addr="${arg#*=}"
    pem="$(openssl s_client -connect "${addr}" -servername "${sni}" ${CA_FILE:+-CAfile "${CA_FILE}"} </dev/null 2>/dev/null \
        | openssl x509 2>/dev/null)"
    if [ -z "${pem}" ]; then
        echo "cert ${node} (${addr}): no certificate (tls_missing)"
        bad=1; continue
    fi
    end="$(openssl x509 -noout -enddate <<<"${pem}" | cut -d= -f2)"
    issuer="$(openssl x509 -noout -issuer <<<"${pem}" | sed 's/^issuer= *//')"
    if ! openssl x509 -noout -checkend $((warn_days * 86400)) <<<"${pem}" >/dev/null; then
        echo "cert ${node} (${addr}): expires ${end}, under ${warn_days} days (issuer ${issuer})"
        bad=1; continue
    fi
    wildcard="*.${sni#*.}"
    sans="$(openssl x509 -noout -ext subjectAltName <<<"${pem}" 2>/dev/null)"
    if ! grep -qF "DNS:${wildcard}" <<<"${sans}"; then
        echo "cert ${node} (${addr}): does not cover ${wildcard} (issuer ${issuer})"
        bad=1; continue
    fi
    echo "cert ${node} (${addr}): ok, ${wildcard}, expires ${end} (issuer ${issuer})"
done
exit "${bad}"
