#!/usr/bin/env bash
# A site's deny rule runs before anything is served (Epic 50, stories 50.31 and 50.11): in
# `caddy adapt`'s JSON, the site's route that answers <status> to the requests it refuses comes
# before every route that reverse-proxies or serves files. Caddy orders handle before respond,
# so a bare "respond @matcher <status>" beside a catch-all handle sorts last and never runs
# (the aop fallback's 403 for addresses outside Cloudflare's ranges, #927; the witness's 401
# without its token). A deny that runs is a handle of its own, written first.
#
#   deny-first.sh <adapted.json> <host> <status> [matcher]
# matcher, when given, is a key the deny route's match must use (remote_ip for the range
# check, header for a token), so an unrelated matched route answering <status> doesn't pass.
set -euo pipefail
adapted="${1:?the adapted JSON}" host="${2:?the site host}" status="${3:?the deny status}" matcher="${4:-}"
[ -s "${adapted}" ] || { echo "deny-first: ${adapted} is empty (caddy adapt failed?)" >&2; exit 1; }
jq -e -r --arg host "${host}" --arg status "${status}" --arg matcher "${matcher}" '
  [.apps.http.servers[].routes[] | select([.match[]?.host[]?] | index($host))][0]
  | if . == null then error("no site for \($host)") else .handle[0].routes end
  | to_entries
  | map({
      i: .key,
      deny: ((.value.match // []) != []
             and ($matcher == "" or ([.value.match | .. | objects | has($matcher)] | any))
             and ([.value | .. | objects | select(.handler == "static_response") | .status_code | tostring] | index($status) != null)),
      serve: ([.value | .. | objects | select(.handler == "reverse_proxy" or .handler == "file_server")] | length > 0)
    })
  | (map(select(.deny)) | first | .i) as $deny
  | (map(select(.serve)) | first | .i) as $serve
  | if $deny == null then error("\($host): no route\(if $matcher != "" then " matching on " + $matcher else "" end) answers \($status) to the requests it refuses")
    elif $serve != null and $deny > $serve then error("\($host): the \($status) comes after a route that serves (route \($serve)): it never runs")
    else "\($host): the \($status) runs first (route \($deny))" end
' "${adapted}"
