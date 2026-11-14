#!/usr/bin/env bash
# Signs the Samsung package's stage (package_wgt.py's OUT/stage) with Tizen's CLI (story 58.10).
#
#   sign_wgt.sh STAGE OUT.wgt
#
# Run by tv-web-release.yml on an Ubuntu runner, as a non-root user (the CLI refuses root), inside
# `dbus-run-session`: the CLI keeps certificate passwords in the desktop keyring (libsecret), so a
# gnome-keyring is unlocked first; without one it prompts on a console and fails on a runner.
#
# The certificates come from the environment only, never the repository:
#   TIZEN_AUTHOR_P12_BASE64, TIZEN_AUTHOR_PASSWORD             the project's Samsung author
#                                                               certificate (Samsung Certificate
#                                                               Manager, the owner's account)
#   TIZEN_DISTRIBUTOR_P12_BASE64, TIZEN_DISTRIBUTOR_PASSWORD   the Samsung distributor certificate
# Without them, SIGN_REHEARSAL=1 signs with a throwaway author certificate and Tizen's own
# distributor certificate (the CLI's, with its published password), through the same commands:
# it proves the path on every tag, and its package is never published
# (no Samsung TV outside Developer Mode installs it). Otherwise it exits 2.
#
# The CLI is Tizen Studio's web CLI, pinned by version and sha256 (TIZEN_CLI_URL, TIZEN_CLI_SHA256).
set -euo pipefail

stage="${1:?STAGE}"
out="${2:?OUT.wgt}"
TIZEN_CLI_URL="${TIZEN_CLI_URL:-https://download.tizen.org/sdk/Installer/tizen-studio_6.1/web-cli_Tizen_Studio_6.1_ubuntu-64.bin}"
TIZEN_CLI_SHA256="${TIZEN_CLI_SHA256:-7f33fe09eda4eb5d50463cf997e8f676d455060720470070ae1932994413aaf4}"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

if [ -n "${TIZEN_AUTHOR_P12_BASE64:-}" ]; then
    mode=release
    for v in TIZEN_AUTHOR_PASSWORD TIZEN_DISTRIBUTOR_P12_BASE64 TIZEN_DISTRIBUTOR_PASSWORD; do
        if [ -z "${!v:-}" ]; then
            echo "sign_wgt: TIZEN_AUTHOR_P12_BASE64 is set but ${v} is not" >&2
            exit 2
        fi
    done
elif [ "${SIGN_REHEARSAL:-}" = 1 ]; then
    mode=rehearsal
else
    echo "sign_wgt: no Samsung certificates (TIZEN_AUTHOR_P12_BASE64) and no SIGN_REHEARSAL=1" >&2
    exit 2
fi

# the CLI, checked before it runs
cli="${HOME}/tizen-studio/tools/ide/bin/tizen"
if [ ! -x "${cli}" ]; then
    curl -sSfL --retry 3 -o "${work}/web-cli.bin" "${TIZEN_CLI_URL}"
    echo "${TIZEN_CLI_SHA256}  ${work}/web-cli.bin" | sha256sum -c -
    chmod +x "${work}/web-cli.bin"
    "${work}/web-cli.bin" --accept-license "${HOME}/tizen-studio" > "${work}/install.log" 2>&1 || {
        tail -30 "${work}/install.log" >&2
        exit 1
    }
fi
"${cli}" version

# the keyring the CLI keeps passwords in (see the header)
eval "$(printf 'maktaba\n' | gnome-keyring-daemon --unlock --components=secrets)"
export GNOME_KEYRING_CONTROL

keys="${work}/keys"
mkdir -p "${keys}"
if [ "${mode}" = rehearsal ]; then
    # a throwaway author certificate and Tizen's own distributor certificate, through the
    # same command a release runs
    pass="rehearsal-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
    "${cli}" certificate -a maktaba-rehearsal -p "${pass}" -c US -n "Maktaba rehearsal" \
        -f maktaba-rehearsal > /dev/null
    author="${HOME}/tizen-studio-data/keystore/author/maktaba-rehearsal.p12"
    TIZEN_AUTHOR_P12_BASE64="$(base64 -w0 "${author}")"
    TIZEN_AUTHOR_PASSWORD="${pass}"
    rm -f "${author}"
    TIZEN_DISTRIBUTOR_P12_BASE64="$(base64 -w0 "${HOME}/tizen-studio/tools/certificate-generator/certificates/distributor/tizen-distributor-signer.p12")"
    TIZEN_DISTRIBUTOR_PASSWORD="tizenpkcs12passfordsigner"
fi
printf '%s' "${TIZEN_AUTHOR_P12_BASE64}" | base64 -d > "${keys}/author.p12"
printf '%s' "${TIZEN_DISTRIBUTOR_P12_BASE64}" | base64 -d > "${keys}/distributor.p12"
"${cli}" security-profiles add -n maktaba \
    -a "${keys}/author.p12" -p "${TIZEN_AUTHOR_PASSWORD}" \
    -d "${keys}/distributor.p12" -dp "${TIZEN_DISTRIBUTOR_PASSWORD}" > /dev/null
"${cli}" cli-config "profiles.path=${HOME}/tizen-studio-data/profile/profiles.xml" > /dev/null

copy="${work}/stage"
cp -R "${stage}" "${copy}"
if ! "${cli}" package -t wgt -s maktaba -- "${copy}"; then
    grep -v '^\s*at ' "${HOME}/tizen-studio-data/cli/logs/cli.log" | tail -20 >&2 || true
    exit 1
fi
built="$(find "${copy}" -maxdepth 1 -name '*.wgt' | head -1)"
if [ -z "${built}" ]; then
    echo "sign_wgt: the CLI wrote no .wgt" >&2
    exit 1
fi
mkdir -p "$(dirname "${out}")"
cp "${built}" "${out}"
# the keys never outlive the run (the trap removes the work folder; the CLI's copy of the
# profile names files that are gone)
rm -f "${HOME}/tizen-studio-data/profile/profiles.xml"
echo "sign_wgt: ${out} (${mode})"
