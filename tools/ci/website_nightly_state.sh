#!/usr/bin/env bash
# The latest nightly quality sweep of maktaba.tv on main (website-nightly.yml, story 60.04), for
# the deploy (website-deploy.yml's nightly job). Writes to $GITHUB_OUTPUT:
#   state=green       the latest sweep that finished passed
#   state=none        no sweep has finished yet
#   state=red         it failed; failed=<JSON list of the pages under the bar> (its
#                     website-nightly-failures artifact): the deploy's build measures them again
#                     and deploys only when they pass
#   state=unreadable  it failed and its list of pages cannot be read: the deploy refuses
#   run=<url>         the sweep's run
# Cancelled and skipped runs are not verdicts: the latest one that passed, failed or timed out
# counts. An API error fails the step (the deploy then runs nothing).
#
#   GH_TOKEN=... REPO=owner/name GITHUB_OUTPUT=file tools/ci/website_nightly_state.sh
set -euo pipefail

: "${REPO:?REPO=owner/name}"
: "${GITHUB_OUTPUT:?}"
WORKFLOW=website-nightly.yml

out() { echo "$1" >> "$GITHUB_OUTPUT"; }

if ! runs=$(gh api "repos/$REPO/actions/workflows/$WORKFLOW/runs?branch=main&status=completed&per_page=30" 2> "${TMPDIR:-/tmp}/nightly-api.err"); then
  if grep -q "HTTP 404" "${TMPDIR:-/tmp}/nightly-api.err"; then
    echo "::notice title=maktaba.tv nightly::no nightly sweep workflow on main yet"
    out "state=none"
    out "failed=[]"
    exit 0
  fi
  cat "${TMPDIR:-/tmp}/nightly-api.err" >&2
  echo "::error::could not read the nightly sweep's runs"
  exit 1
fi

latest=$(jq -c '[.workflow_runs[] | select(.conclusion == "success" or .conclusion == "failure" or .conclusion == "timed_out")][0] // empty' <<< "$runs")
if [ -z "$latest" ]; then
  echo "::notice title=maktaba.tv nightly::no nightly sweep has finished yet"
  out "state=none"
  out "failed=[]"
  exit 0
fi

id=$(jq -r .id <<< "$latest")
conclusion=$(jq -r .conclusion <<< "$latest")
url=$(jq -r .html_url <<< "$latest")
out "run=$url"
if [ "$conclusion" = "success" ]; then
  echo "::notice title=maktaba.tv nightly::the latest sweep passed ($url)"
  out "state=green"
  out "failed=[]"
  exit 0
fi

dir=$(mktemp -d)
if gh run download "$id" -R "$REPO" -n website-nightly-failures -D "$dir" > /dev/null 2>&1 \
  && failed=$(jq -ce 'if type == "array" and length > 0 and all(type == "string" and startswith("/")) then . else error("not a list of pages") end' "$dir/failures.json" 2> /dev/null); then
  echo "::warning title=maktaba.tv nightly::the latest sweep failed ($url) on $(jq -r 'join(", ")' <<< "$failed"): this deploy measures those pages again and deploys only if they pass"
  out "state=red"
  out "failed=$failed"
else
  echo "::error title=maktaba.tv nightly::the latest sweep failed ($url) and its list of failing pages cannot be read: the deploy refuses until a sweep passes (dispatch website-nightly.yml after the fix)"
  out "state=unreadable"
  out "failed=[]"
fi
