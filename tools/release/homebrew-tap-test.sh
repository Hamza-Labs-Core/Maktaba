#!/usr/bin/env bash
# Checks homebrew-tap.sh: the formula and the cask are filled (their header comments keep
# the placeholders' names), parse as Ruby (when ruby is here), and refuse a version or a
# digest that is not one; push commits them into a clone of a scratch tap once, says so on a
# second push, and never takes the tap back to an older release. Touches nothing outside
# its temporary directory.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
tap() { bash "${here}/homebrew-tap.sh" "$@"; }

a="$(printf 'a%.0s' {1..64})"
b="$(printf 'b%.0s' {1..64})"
c="$(printf 'c%.0s' {1..64})"

echo "the formula and the cask"
tap formula 0.3.7 "${a}" "${b}" >"${work}/maktaba.rb"
tap cask 0.3.7 "${c}" >"${work}/maktaba-app.rb"
if grep -q '^  version "0.3.7"$' "${work}/maktaba.rb" && grep -q "sha256 \"${a}\"" "${work}/maktaba.rb" &&
    grep -q "sha256 \"${b}\"" "${work}/maktaba.rb"; then
    ok "the formula names the version and both archives' digests"
else
    fail "the formula: $(grep -n 'version\|sha256' "${work}/maktaba.rb")"
fi
if grep -q '^  version "0.3.7"$' "${work}/maktaba-app.rb" && grep -q "sha256 \"${c}\"" "${work}/maktaba-app.rb"; then
    ok "the cask names the version and the app's digest"
else
    fail "the cask: $(grep -n 'version\|sha256' "${work}/maktaba-app.rb")"
fi
if grep -v '^[[:space:]]*#' "${work}/maktaba.rb" "${work}/maktaba-app.rb" | grep -q '__[A-Z0-9_]*__'; then
    fail "a placeholder is left"
elif grep -q '__VERSION__' "${work}/maktaba.rb"; then
    ok "every placeholder is filled; the header comments keep their names"
else
    fail "the header comment lost the placeholders' names"
fi
if grep -q 'run \[opt_libexec/"maktaba-service"\]' "${work}/maktaba.rb" &&
    grep -q -- '--identity package' "${work}/maktaba.rb"; then
    ok "the formula's service stages its release and runs the launcher (it updates itself)"
else
    fail "the formula's service does not run the launcher"
fi
if tap formula 0.3.7-rc.1 "${a}" "${b}" >/dev/null; then
    ok "a pre-release's formula is filled (the release attaches it)"
else
    fail "a pre-release's formula"
fi
for bad in "1.2" "v0.3.7" "0.3.7;rm" ; do
    if tap formula "${bad}" "${a}" "${b}" >/dev/null 2>&1; then fail "version ${bad} taken"; else ok "version ${bad} refused"; fi
done
if tap cask 0.3.7 "nothex" >/dev/null 2>&1; then fail "a digest that is not one taken"; else ok "a digest that is not one refused"; fi
if command -v ruby >/dev/null 2>&1; then
    if ruby -c "${work}/maktaba.rb" >/dev/null && ruby -c "${work}/maktaba-app.rb" >/dev/null; then
        ok "both parse as Ruby"
    else
        fail "ruby -c"
    fi
else
    printf '  skip  ruby -c (no ruby here)\n'
fi

echo "push"
git init -q --bare "${work}/tap.git"
git clone -q "${work}/tap.git" "${work}/clone" 2>/dev/null
out="$(tap push "${work}/clone" v0.3.7 "${work}/maktaba.rb" "${work}/maktaba-app.rb")"
git clone -q "${work}/tap.git" "${work}/check"
if grep -q "now has v0.3.7" <<<"${out}" && cmp -s "${work}/check/Formula/maktaba.rb" "${work}/maktaba.rb" &&
    cmp -s "${work}/check/Casks/maktaba-app.rb" "${work}/maktaba-app.rb" &&
    [ "$(git -C "${work}/check" log -1 --format=%s)" = "maktaba v0.3.7" ]; then
    ok "the formula and the cask are committed and pushed"
else
    fail "push: ${out}"
fi
out="$(tap push "${work}/clone" v0.3.7 "${work}/maktaba.rb" "${work}/maktaba-app.rb")"
if grep -q "already" <<<"${out}"; then ok "a second push changes nothing"; else fail "a second push: ${out}"; fi
tap formula 0.3.6 "${b}" "${a}" >"${work}/old.rb"
tap cask 0.3.6 "${a}" >"${work}/old-app.rb"
out="$(tap push "${work}/clone" v0.3.6 "${work}/old.rb" "${work}/old-app.rb")"
if grep -q "newer release (0.3.7): kept" <<<"${out}" && grep -q '"0.3.7"' "${work}/clone/Formula/maktaba.rb"; then
    ok "an older release never takes the tap back"
else
    fail "an older release: ${out}"
fi
if tap push "${work}/clone" v0.3.8 "${work}/maktaba.rb" >/dev/null 2>&1; then
    fail "another tag's formula pushed"
else
    ok "a formula that is not the tag's is refused"
fi

echo
if [ "${fails}" -gt 0 ]; then
    echo "homebrew-tap-test.sh: ${fails} failed"
    exit 1
fi
echo "homebrew-tap-test.sh: all passed"
