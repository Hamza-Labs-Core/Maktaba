#!/usr/bin/env bash
# Checks release-assets.sh adopt against a stubbed gh: a published release's bundle and its
# .sig are taken together; a leg the release never had keeps this run's files; a release
# published with a bundle but not its signature (or the other way round) is refused, the run's
# files left as they were; a draft is left alone. Needs bash; touches nothing outside its
# temporary directory.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stubbed gh: one release (id 7, DRAFT true|false) whose assets are ASSETS (one per line);
# a download writes "published <name>" into the file
mkdir -p "${work}/stubs"
cat >"${work}/stubs/gh" <<'EOF'
#!/usr/bin/env bash
case "$1" in
    api)
        case "$2" in
            *"/releases?per_page=100") echo 7 ;;
            *"/releases/7") echo "${DRAFT}" ;;
            *"/releases/7/assets?per_page=100") printf '%s\n' "${ASSETS}" ;;
            *) echo "gh stub: $*" >&2; exit 1 ;;
        esac
        ;;
    release)
        [ "$2" = download ] || { echo "gh stub: $*" >&2; exit 1; }
        name="" dir=""
        while [ $# -gt 0 ]; do
            case "$1" in
                --pattern) name="$2"; shift ;;
                --dir) dir="$2"; shift ;;
            esac
            shift
        done
        printf 'published %s' "${name}" >"${dir}/${name}"
        ;;
    *) echo "gh stub: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${work}/stubs/gh"

# stage NAME...: a run's staged directory, each file "built <name>"
stage() {
    rm -rf "${work}/staged"
    mkdir -p "${work}/staged"
    local n
    for n in "$@"; do
        printf 'built %s' "${n}" >"${work}/staged/${n}"
    done
}
content() { cat "${work}/staged/$1"; }
adopt() { # adopt DRAFT ASSETS: runs adopt on the staged directory; its log in ${work}/log
    PATH="${work}/stubs:${PATH}" GITHUB_REPOSITORY=o/r DRAFT="$1" ASSETS="$2" \
        bash "${here}/release-assets.sh" adopt v0.3.2 "${work}/staged" >"${work}/log" 2>&1
}

app=Maktaba.app.tar.gz
exe=Maktaba_0.3.2_x64-setup.exe

stage "${app}" "${app}.sig" "${exe}" "${exe}.sig" latest.json
if adopt false "${app}"$'\n'"${app}.sig"$'\n'latest.json &&
    [ "$(content "${app}")" = "published ${app}" ] && [ "$(content "${app}.sig")" = "published ${app}.sig" ] &&
    [ "$(content "${exe}")" = "built ${exe}" ] && [ "$(content "${exe}.sig")" = "built ${exe}.sig" ] &&
    [ "$(content latest.json)" = "built latest.json" ]; then
    ok "a published pair is taken together; a new leg and latest.json keep this run's"
else
    fail "pair taken: $(cat "${work}/log")"
fi

# the players' bundles and feed (Epic 42.4) go the same way
msi=Maktaba-Player_0.3.2_x64.msi
stage "${msi}" "${msi}.sig" latest-player.json
if adopt false "${msi}"$'\n'"${msi}.sig"$'\n'latest-player.json &&
    [ "$(content "${msi}")" = "published ${msi}" ] && [ "$(content "${msi}.sig")" = "published ${msi}.sig" ] &&
    [ "$(content latest-player.json)" = "built latest-player.json" ]; then
    ok "a published player pair is taken together; latest-player.json keeps this run's"
else
    fail "player pair: $(cat "${work}/log")"
fi

stage "${app}" "${app}.sig"
if adopt false "${app}"; then
    fail "a published bundle without its signature was adopted: $(cat "${work}/log")"
elif grep -q "published with ${app} but not ${app}.sig" "${work}/log" &&
    [ "$(content "${app}")" = "built ${app}" ] && [ "$(content "${app}.sig")" = "built ${app}.sig" ]; then
    ok "a bundle published without its signature is refused, nothing mixed"
else
    fail "bundle without signature: $(cat "${work}/log")"
fi

stage "${app}" "${app}.sig"
if adopt false "${app}.sig"; then
    fail "a published signature without its bundle was adopted: $(cat "${work}/log")"
elif grep -q "published with ${app}.sig but not ${app}" "${work}/log" &&
    [ "$(content "${app}")" = "built ${app}" ]; then
    ok "a signature published without its bundle is refused"
else
    fail "signature without bundle: $(cat "${work}/log")"
fi

stage "${app}" "${app}.sig"
if adopt true "${app}" && [ "$(content "${app}")" = "built ${app}" ]; then
    ok "a draft's files are this run's"
else
    fail "draft: $(cat "${work}/log")"
fi

stage Maktaba.dmg
if adopt false Maktaba.dmg && [ "$(content Maktaba.dmg)" = "published Maktaba.dmg" ]; then
    ok "a file with no signature is taken alone, as before"
else
    fail "unsigned file: $(cat "${work}/log")"
fi

if [ "${fails}" -gt 0 ]; then
    echo "release-assets-test: ${fails} failed"
    exit 1
fi
echo "release-assets-test: all passed"
