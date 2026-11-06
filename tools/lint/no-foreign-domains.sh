#!/usr/bin/env bash
# No hostname under a domain that is not ours (story 50.01).
#
# maktaba.tv is for users, maktaba.dev for development, the wiki and staging. maktaba.app is
# NOT ours: a copied example pointing at it sends people to a domain someone else owns. This
# check fails when a changed file adds a hostname under a foreign domain:
#   - a URL:              https://maktaba.app/…, wss://relay.maktaba.app
#   - a host name:        skylark-12.relay.maktaba.app
# and lets through reverse-DNS app identifiers, which never resolve and stay:
#   - com.maktaba.app, io.maktaba.app (bundle and application ids)
# and the domain named on its own in prose ("maktaba.app is not ours").
#
# Usage:
#   no-foreign-domains.sh [BASE]     check the lines added since BASE (default: the merge
#                                    base with origin/main), as the lint's repo job does
#   no-foreign-domains.sh --stdin    check the text on stdin (each line as "path:line:text")
#
# If HamzaLabs ever owns one of these domains, FOREIGN below is the one place to change.
set -euo pipefail

# the domains that are not ours (extended regex, dots escaped)
FOREIGN='maktaba\.app'
# where a name ends: not followed by another label (com.maktaba.app.debug is an id, and
# maktaba.application another word); a trailing dot ends a sentence
END='(\.?([^a-z0-9.-]|$))'

# Paths never checked, each with its reason.
EXEMPT=(
  # this check and its test hold the bad names on purpose
  'tools/lint/no-foreign-domains.sh'
  'tools/lint/no-foreign-domains-test.sh'
  # generated from the specs by docs/wiki/db/generate_wiki.py (the specs themselves are checked)
  'web/wiki-app/public/db/*'
  # Epic 50's own spec (and its Arabic) names the stale name it removes, as an example
  'specs/epics/50-cloud-no-downtime/*'
  'docs/wiki/db/ar/epic-50.json'
)

# Prints "path:line: text" for each offending line it reads as "path:line:text" on stdin.
scan() {
  local bad=0 path rest lineno text label
  while IFS= read -r entry; do
    path=${entry%%:*}; rest=${entry#*:}; lineno=${rest%%:*}; text=${rest#*:}
    for ex in "${EXEMPT[@]}"; do
      # shellcheck disable=SC2053  # a glob on purpose
      [[ $path == $ex ]] && continue 2
    done
    # a URL with the domain as its host (or a subdomain of it)
    if grep -qiE "[a-z][a-z0-9+.-]*://([a-z0-9-]+\.)*${FOREIGN}${END}" <<<"$text"; then
      echo "$path:$lineno: adds a ${FOREIGN//\\/} URL: $text"; bad=1; continue
    fi
    # a host name: a label, then the domain; com.maktaba.app / io.maktaba.app are ids
    while IFS= read -r label; do
      [ -n "$label" ] || continue
      # the label just before the domain: "skylark-12.relay" -> "relay", "com" -> "com"
      label=$(tr '[:upper:]' '[:lower:]' <<<"$label" | sed -E "s/\.${FOREIGN}.*$//")
      label=${label##*.}
      case "$label" in com|io|org|net) continue ;; esac
      echo "$path:$lineno: adds a ${FOREIGN//\\/} host name: $text"; bad=1; continue 2
    done < <(grep -oiE "([a-z0-9-]+\.)+${FOREIGN}${END}" <<<"$text" || true)
  done < <(grep -iE "$FOREIGN" || true)
  return $bad
}

if [ "${1:-}" = "--stdin" ]; then
  scan
  exit $?
fi

base=${1:-$(git merge-base origin/main HEAD)}
# "path:line:text" for every line the diff adds (the new file's line numbers)
git diff --unified=0 --no-color --diff-filter=AM "$base" HEAD | awk '
  /^\+\+\+ / { path = substr($0, 7); next }
  /^@@ / { split($3, a, ","); line = substr(a[1], 2) + 0; next }
  /^\+/ { print path ":" line ":" substr($0, 2); line++ }
' | scan || {
  echo "::error::a changed file adds a host name under a domain that is not ours (see tools/lint/no-foreign-domains.sh)"
  exit 1
}
