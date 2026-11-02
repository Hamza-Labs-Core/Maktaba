#!/usr/bin/env bash
# Checks tools/release/play-version.sh: the versionCode grows with every tag (pre-releases
# included, so no two tags share one), stays within Google Play's limit, and a tag that cannot
# be one is refused. Needs only bash.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

want() { # TAG NAME CODE
    local out
    if ! out="$(bash "${here}/play-version.sh" "$1" 2>&1)"; then
        fail "$1: refused (${out})"
        return
    fi
    if [ "${out}" = "$(printf 'name=%s\ncode=%s' "$2" "$3")" ]; then
        ok "$1 -> $2 ($3)"
    else
        fail "$1: got ${out//$'\n'/ }, want name=$2 code=$3"
    fi
}

refused() { # TAG
    if bash "${here}/play-version.sh" "$1" >/dev/null 2>&1; then
        fail "'$1': accepted, want refused"
    else
        ok "'$1' refused"
    fi
}

code_of() { bash "${here}/play-version.sh" "$1" | sed -n 's/^code=//p'; }

want v0.3.44 0.3.44 304499
want v0.3.55 0.3.55 305599
want v0.4.0 0.4.0 400099
want v0.4.0-alpha.1 0.4.0-alpha.1 400001
want v0.4.0-alpha.32 0.4.0-alpha.32 400032
want v0.4.0-beta.1 0.4.0-beta.1 400033
want v0.4.0-beta.32 0.4.0-beta.32 400064
want v0.4.0-rc.1 0.4.0-rc.1 400065
want v0.4.0-rc.34 0.4.0-rc.34 400098
want v0.0.1 0.0.1 199
want v1.0.0 1.0.0 100000099
want v1.2.3 1.2.3 100200399
want v0.10.9 0.10.9 1000999
want v20.999.999 20.999.999 2099999999

refused 0.3.45
refused v0.3
refused v0.3.45.1
refused v0.3.08
refused v0.1000.0
refused v0.3.1000
refused v21.0.0
refused v99999.0.0
refused v0.0.0
refused v0.0.0-rc.1
refused 'v1.2.3 '
refused ''
refused v0.4.0-rc
refused v0.4.0-rc.0
refused v0.4.0-rc.01
refused v0.4.0-rc.35
refused v0.4.0-alpha.33
refused v0.4.0-beta.33
refused v0.4.0-preview.1
refused v0.4.0-rc.1.1
refused v0.4.0-RC.1

# every tag's code is above the one before it (semver order, the order releases are cut in), so
# no two tags share one; the first is above v0.3.54's code under the old rule (3054)
prev=3054
for t in v0.3.55 v0.3.999 v0.4.0-alpha.1 v0.4.0-alpha.2 v0.4.0-beta.1 v0.4.0-rc.1 v0.4.0-rc.2 \
    v0.4.0-rc.10 v0.4.0 v0.4.1-rc.1 v0.4.1 v0.999.999 v1.0.0-rc.1 v1.0.0 v1.0.1 v1.1.0 v2.0.0 \
    v20.999.999; do
    code="$(code_of "${t}")"
    if [ "${code}" -gt "${prev}" ]; then
        ok "${t} (${code}) above the tag before"
    else
        fail "${t} (${code}) not above ${prev}"
    fi
    prev="${code}"
done

# the Capacitor app (mobile-release.yml) kept the old rule one tag longer: v0.3.55 was its
# highest code, 3055 (run 41498853328's "Stamp Android version from tag"). Every tag after it,
# its first possible pre-release included, is above that, so installs of it still update with
# no offset
for t in v0.3.55 v0.3.56-alpha.1 v0.3.56 v0.4.0-alpha.1; do
    code="$(code_of "${t}")"
    if [ "${code}" -gt 3055 ]; then
        ok "${t} (${code}) above the Capacitor app's last old code (3055)"
    else
        fail "${t} (${code}) not above the Capacitor app's last old code (3055)"
    fi
done

# a pre-release and its version (once the same code) and the repository's own tags: all distinct
codes=""
for t in v0.1.0-rc.1 v0.1.0-rc.2 v0.1.0-rc.3 v0.1.0 v0.3.0-rc.1 v0.3.0-rc.7 v0.3.0 v0.3.45 v0.3.54; do
    codes+="$(code_of "${t}")"$'\n'
done
dups="$(sort <<<"${codes}" | uniq -d | tr '\n' ' ')"
if [ -z "${dups// /}" ]; then
    ok "pre-releases and releases have distinct codes"
else
    fail "codes repeat: ${dups}"
fi

[ "${fails}" -eq 0 ] || { echo "${fails} failed"; exit 1; }
echo "all passed"
