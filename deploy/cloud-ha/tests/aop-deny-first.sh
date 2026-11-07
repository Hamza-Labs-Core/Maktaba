#!/usr/bin/env bash
# The aop fallback's deny rule runs before anything is proxied (Epic 50 story 50.31): in
# `caddy adapt`'s JSON of the pair's Caddyfile with origin.d/aop.caddy, the api.maktaba.tv
# site's route that answers 403 to an address outside Cloudflare's ranges comes before every
# route that reverse-proxies. A bare "respond @outside 403" fails this: Caddy orders handle
# before respond, so the catch-all handle answered every request and the 403 never ran.
#
#   aop-deny-first.sh <adapted.json>
set -euo pipefail
adapted="${1:?the adapted JSON}"
[ -s "${adapted}" ] || { echo "aop-deny-first: ${adapted} is empty (caddy adapt failed?)" >&2; exit 1; }
jq -e -r '
  [.apps.http.servers[].routes[] | select([.match[]?.host[]?] | index("api.maktaba.tv"))][0].handle[0].routes
  | to_entries
  | map({
      i: .key,
      deny: (((.value.match // []) | tostring | test("remote_ip"))
             and ([.value | .. | objects | select(.handler == "static_response") | .status_code | tostring] | index("403") != null)),
      proxy: ([.value | .. | objects | select(.handler == "reverse_proxy")] | length > 0)
    })
  | (map(select(.deny)) | first | .i) as $deny
  | (map(select(.proxy)) | first | .i) as $proxy
  | if $deny == null then error("no route answers 403 to an address outside the ranges")
    elif $proxy != null and $deny > $proxy then error("the 403 for addresses outside the ranges comes after a reverse_proxy route: it never runs")
    else "aop: the 403 for addresses outside the ranges runs first (route \($deny))" end
' "${adapted}"
