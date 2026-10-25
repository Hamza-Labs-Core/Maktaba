#!/usr/bin/env bash
# Checks tools/release/tag-guard.sh against a scratch repository and a stubbed gh: a tag
# above the newest release on main passes (and is the latest); one on main that does not
# contain the newest release's commit is refused; a re-run of a published tag passes as
# published; a maintenance release passes from a release/* branch only, never the latest;
# a pre-release of a version already out is refused. Needs git, go and bash; touches
# nothing outside its temporary directory.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stubbed gh: the published releases (PUBLISHED, one tag per line) and a passed CI run
mkdir -p "${work}/stubs"
cat >"${work}/stubs/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
    "release list") printf '%s\n' "${PUBLISHED}" ;;
    "run list") echo "completed success" ;;
    *) echo "gh stub: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${work}/stubs/gh"

# the repository: main = v0.3.0 -> v0.3.1, and a side commit merged into main that does not
# contain v0.3.1; release/0.2 from before v0.3.0; an orphan commit on no branch
git init -q --bare "${work}/origin.git"
git clone -q "${work}/origin.git" "${work}/repo" 2>/dev/null
cd "${work}/repo"
git config user.email t@example.com
git config user.name test
git config commit.gpgsign false
git config tag.gpgsign false
commit() { echo "$1" >"file-$1"; git add "file-$1"; git commit -q -m "$1"; git rev-parse HEAD; }
git symbolic-ref HEAD refs/heads/main # whatever init.defaultBranch says
c0="$(commit base)"
c1="$(commit v0.3.0)"; git tag v0.3.0
c2="$(commit v0.3.1)"; git tag v0.3.1
git checkout -q -b side "${c1}"
s1="$(commit side)"
git checkout -q main
git merge -q --no-edit side >/dev/null
c3="$(commit next)"
git checkout -q -b release/0.2 "${c0}"
m1="$(commit hotfix)"
git checkout -q --orphan lost
o1="$(commit orphan)"
git checkout -q main
git push -q origin main release/0.2 --tags 2>/dev/null

# guard TAG SHA [ALLOW]: runs the guard; its outputs in ${work}/out, its log in ${work}/log
guard() {
    : >"${work}/out"
    PATH="${work}/stubs:${PATH}" TAG="$1" SHA="$2" ALLOW_TAG="${3:-}" GITHUB_OUTPUT="${work}/out" \
        PUBLISHED=$'v0.2.0\nv0.3.0\nv0.3.1' CI_WAIT_SECONDS=5 \
        bash "${here}/tag-guard.sh" >"${work}/log" 2>&1
}
out() { sed -n "s/^$1=//p" "${work}/out"; }
expect_pass() { # expect_pass NAME TAG SHA ALLOW published latest maintenance
    if guard "$2" "$3" "$4" && [ "$(out published)" = "$5" ] && [ "$(out latest)" = "$6" ] &&
        [ "$(out maintenance)" = "$7" ]; then
        ok "$1"
    else
        fail "$1: $(cat "${work}/log") / $(tr '\n' ' ' <"${work}/out")"
    fi
}
expect_refused() { # expect_refused NAME TAG SHA ALLOW WORDS
    if guard "$2" "$3" "$4"; then
        fail "$1: passed"
    elif grep -q "$5" "${work}/log"; then
        ok "$1"
    else
        fail "$1: refused, but not for '$5': $(cat "${work}/log")"
    fi
}

expect_pass "a tag above the newest release on main: the latest" v0.3.2 "${c3}" "" false true false
[ "$(out newest)" = v0.3.1 ] || fail "newest: $(out newest)"
expect_refused "a tag on main without the newest release's commit" v0.3.2 "${s1}" "" "does not contain v0.3.1"
expect_pass "a re-run of a published tag" v0.3.1 "${c2}" "" true true false
expect_pass "a maintenance release from release/0.2: never the latest" v0.2.5 "${m1}" v0.2.5 false false true
expect_refused "a maintenance release on no branch" v0.2.6 "${o1}" v0.2.6 "on neither main nor a release/"
expect_refused "a pre-release of a version already out" v0.3.0-rc.7 "${c3}" "" "pre-release below"
expect_refused "a tag that is not on main" v0.3.2 "${m1}" "" "not on main"

if [ "${fails}" -gt 0 ]; then
    echo "tag-guard-test: ${fails} failed"
    exit 1
fi
echo "tag-guard-test: all passed"
