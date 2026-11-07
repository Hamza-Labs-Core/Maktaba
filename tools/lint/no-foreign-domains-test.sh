#!/usr/bin/env bash
# Tests for no-foreign-domains.sh (story 50.01, T01): host names and URLs fail, reverse-DNS
# app identifiers and the bare domain in prose pass.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
check="$here/no-foreign-domains.sh"
fails=0

expect() { # expect pass|fail "line"
  local want=$1 text=$2 got
  if printf 'x.txt:1:%s\n' "$text" | "$check" --stdin >/dev/null; then got=pass; else got=fail; fi
  if [ "$got" != "$want" ]; then
    echo "FAIL: want $want, got $got: $text"; fails=$((fails + 1))
  else
    echo "ok   $want: $text"
  fi
}

expect fail 'host = "skylark-12.relay.maktaba.app"'
expect fail '// see https://maktaba.app/schema/line.json'
expect fail 'url: wss://relay.maktaba.app/v1/relay/ws'
expect fail '# https://foo.maktaba.app in a comment'
expect fail 'API.MAKTABA.APP is the host'
expect pass 'bundle_id = "com.maktaba.app"'
expect pass "appId: 'io.maktaba.app'"
expect pass 'applicationId "com.maktaba.app.debug"'
expect pass 'maktaba.app is not ours; users get maktaba.tv'
expect pass 'https://relay.maktaba.tv and https://maktaba.dev'
expect pass 'maktaba.application is a different word'
expect pass 'https://maktaba.appspot.example'
expect fail 'the host is foo.maktaba.app.'
expect pass 'Built for COM.MAKTABA.APP too'

# an exempt path is never checked
if printf 'web/wiki-app/public/db/x.json:1:https://foo.maktaba.app\n' | "$check" --stdin >/dev/null; then
  echo "ok   exempt path"
else
  echo "FAIL: an exempt path was checked"; fails=$((fails + 1))
fi

# the diff mode names the file and the line
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
git -C "$tmp" init -q -b main && git -C "$tmp" -c user.email=t@t -c user.name=t commit -q --allow-empty -m base
printf 'one\ntwo https://foo.maktaba.app\n' >"$tmp/a.md"
git -C "$tmp" add a.md && git -C "$tmp" -c user.email=t@t -c user.name=t commit -q -m add
out=$(cd "$tmp" && "$check" HEAD~1 2>&1); rc=$?
if [ $rc -ne 0 ] && grep -q '^a.md:2:' <<<"$out"; then
  echo "ok   diff mode names a.md:2"
else
  echo "FAIL: diff mode: rc=$rc out=$out"; fails=$((fails + 1))
fi

if [ "$fails" -ne 0 ]; then echo "$fails failed"; exit 1; fi
echo "all passed"
