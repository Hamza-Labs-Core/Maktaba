#!/usr/bin/env bash
# The Docker image as a NAS runs it (Epic 59 story 59.01): any uid:gid, never root, first start
# offline. On the image CI just built from this payload (_linux-app.yml, "The Docker image"):
#
#   T01/T03/T06  for each of 1000:1000, 568:568 (TrueNAS), 99:100 (Unraid), 1026:100 (a DSM
#                user) and 4321:4321 (no entry in the image's /etc/passwd): a fresh volume owned
#                by that user, `setup --answers --bundle core` with --network none (the venv
#                from the payload's wheelhouse, no PyPI; initdb under nss_wrapper for a uid the
#                image does not name), then the container itself, still offline: the API
#                answers, the plugin runners start, and every process runs as that uid:gid
#   T02          a root-owned volume (755) started as 568: exit 78 within 5 s, the message in
#                English and Arabic naming the folder and the uid
#   T04/T05      --privileged, and the Docker socket mounted: refused, exit 78
#                uid 0: refused, exit 78
#   T07 (here)   the image's compressed size, estimated (gzip of `docker save`): 1 GB or less
#                (release.yml checks the pushed manifest's layers)
#
# Every container's output and the volume's logs (setup, venv, server) go to LOGDIR, which
# the job uploads.
#
# Usage: docker-nas.sh IMAGE LOGDIR
set -euo pipefail

IMAGE="${1:?image}"
LOGDIR="${2:?log dir}"
mkdir -p "$LOGDIR"
SHAPES=(1000:1000 568:568 99:100 1026:100 4321:4321)
MAX_COMPRESSED=1000000000

fails=0
log() { printf '[docker-nas] %s\n' "$*"; }
fail() { printf '::error title=docker-nas::%s\n' "$*"; fails=$((fails + 1)); }

volumes=() containers=()
cleanup() {
    for c in ${containers[@]+"${containers[@]}"}; do docker rm -f "$c" >/dev/null 2>&1 || true; done
    for v in ${volumes[@]+"${volumes[@]}"}; do docker volume rm -f "$v" >/dev/null 2>&1 || true; done
}
trap cleanup EXIT

# fresh_volume NAME OWNER MODE: a named volume whose root is OWNER's with MODE. Mounted at
# /var/lib/maktaba once first, so Docker's copy of the image's folder (1000's) happens before
# the owner is set.
fresh_volume() {
    docker volume rm -f "$1" >/dev/null 2>&1 || true
    docker volume create "$1" >/dev/null
    volumes+=("$1")
    docker run --rm --user 0 --entrypoint sh -v "$1":/var/lib/maktaba "$IMAGE" \
        -c "chown -R $2 /var/lib/maktaba && chmod $3 /var/lib/maktaba"
}

# keep_logs NAME VOLUME: the volume's logs into LOGDIR/NAME
keep_logs() {
    mkdir -p "$LOGDIR/$1"
    docker run --rm --user 0 --entrypoint sh -v "$2":/v:ro -v "$LOGDIR/$1":/out "$IMAGE" \
        -c 'cp -r /v/state/maktaba/logs/. /out/ 2>/dev/null; chmod -R a+rX /out' || true
}

# refused NAME EXPECT ARGS...: the container exits 78 within 5 s, saying EXPECT, in Arabic too
refused() {
    local name="$1" expect="$2" out rc start took
    shift 2
    start=$(date +%s)
    rc=0
    out="$(timeout 30 docker run --rm "$@" "$IMAGE" 2>&1)" || rc=$?
    took=$(($(date +%s) - start))
    printf '%s\n' "$out" >"$LOGDIR/refused-$name.log"
    if [ "$rc" -ne 78 ]; then fail "$name: exit $rc, want 78: $(tail -n 3 <<<"$out")"; return; fi
    if [ "$took" -gt 5 ]; then fail "$name: took ${took}s, want 5 s or less"; fi
    if ! grep -qF -- "$expect" <<<"$out"; then fail "$name: no \"$expect\" in: $out"; return; fi
    # every refusal's Arabic line starts with لا (does not / cannot)
    if ! sed -n 2p <<<"$out" | grep -q '^لا .*Maktaba'; then
        fail "$name: no Arabic line after the English: $out"
        return
    fi
    log "$name: refused in ${took}s (exit 78)"
}

# --- refusals ------------------------------------------------------------------------------
fresh_volume nas-ro 0:0 755
refused write-denied "cannot write to its data folder /var/lib/maktaba as user 568 (group 568)" \
    --user 568:568 -v nas-ro:/var/lib/maktaba
refused root "does not run as root" --user 0:0
refused privileged "privileged container" --user 1000:1000 --privileged
refused docker-sock "Docker socket" --user 1000:1000 -v /var/run/docker.sock:/var/run/docker.sock

# --- every uid shape, offline --------------------------------------------------------------
answers="$(mktemp -d)/answers.json"
cat >"$answers" <<'EOF'
{"admin": {"username": "nas", "password": "nas-shapes-not-a-secret-1"},
 "libraries": {"roots": ["/media"], "path_map": []},
 "models": {"source": "huggingface", "hf_token": ""},
 "plex": {"action": "skip"}}
EOF
chmod 644 "$answers"

for shape in "${SHAPES[@]}"; do
    uid="${shape%%:*}" name="uid-${shape/:/-}" vol="nas-${shape/:/-}" ctr="nas-run-${shape/:/-}"
    echo "::group::$shape"
    fresh_volume "$vol" "$shape" 755
    rc=0
    timeout 900 docker run --rm --network none --user "$shape" -v "$vol":/var/lib/maktaba \
        -v "$answers":/answers.json:ro --tmpfs "/media:uid=$uid" \
        "$IMAGE" setup --answers /answers.json --bundle core --lang en >"$LOGDIR/$name-setup.log" 2>&1 || rc=$?
    tail -n 5 "$LOGDIR/$name-setup.log"
    if [ "$rc" -ne 0 ]; then
        fail "$shape: offline setup exit $rc: $(tail -n 3 "$LOGDIR/$name-setup.log")"
        keep_logs "$name" "$vol"
        echo "::endgroup::"
        continue
    fi
    if [ "$shape" != 1000:1000 ] && ! grep -q "nss_wrapper names it maktaba" "$LOGDIR/$name-setup.log"; then
        fail "$shape: the entrypoint did not name the uid (nss_wrapper)"
    fi

    containers+=("$ctr")
    docker run -d --name "$ctr" --network none --user "$shape" -v "$vol":/var/lib/maktaba \
        --tmpfs "/media:uid=$uid" "$IMAGE" >/dev/null
    up=""
    for _ in $(seq 1 90); do
        if docker exec "$ctr" /opt/maktaba/server/python/bin/python3 -I -c \
            'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8080/api/system/version", timeout=2)' 2>/dev/null; then
            up=1
            break
        fi
        sleep 2
    done
    runners=""
    for _ in $(seq 1 60); do
        if docker exec "$ctr" sh -c 'grep -q "started plugin-runner/" /var/lib/maktaba/state/maktaba/logs/server.log' 2>/dev/null; then
            runners=1
            break
        fi
        sleep 2
    done
    sleep 5 # the runners' interpreters are running by now
    # every process's real, effective, saved and filesystem uid and gid (no ps in the image)
    docker exec "$ctr" sh -c 'for d in /proc/[0-9]*; do
            [ -r "$d/status" ] || continue
            u=$(awk "/^Uid:/{print \$2\",\"\$3\",\"\$4\",\"\$5}" "$d/status" 2>/dev/null)
            g=$(awk "/^Gid:/{print \$2\",\"\$3\",\"\$4\",\"\$5}" "$d/status" 2>/dev/null)
            c=$(tr "\0\n" "  " <"$d/cmdline" 2>/dev/null | cut -c1-120)
            [ -n "$u" ] && echo "$u $g $c"
        done' >"$LOGDIR/$name-ps.txt" 2>/dev/null || true
    docker logs "$ctr" >"$LOGDIR/$name-container.log" 2>&1 || true
    keep_logs "$name" "$vol"
    want="$uid,$uid,$uid,$uid ${shape#*:},${shape#*:},${shape#*:},${shape#*:}"
    others="$(awk -v w="$want" '{ if ($1" "$2 != w) print }' "$LOGDIR/$name-ps.txt")"
    count="$(wc -l <"$LOGDIR/$name-ps.txt" | tr -d ' ')"
    if [ -z "$up" ]; then fail "$shape: the API never answered offline (see $name-container.log)"; fi
    if [ -z "$runners" ]; then fail "$shape: no plugin runner started"; fi
    if ! grep -q "postgres -D" "$LOGDIR/$name-ps.txt"; then fail "$shape: Postgres is not running"; fi
    if ! grep -q "maktaba_plugin_runner" "$LOGDIR/$name-ps.txt"; then fail "$shape: no plugin runner process"; fi
    if [ -n "$others" ]; then fail "$shape: processes not running as $shape: $others"; fi
    if grep -qi "could not look up effective user\|ERROR: ld.so" "$LOGDIR"/"$name"/*.log "$LOGDIR/$name-container.log" 2>/dev/null; then
        fail "$shape: a user lookup or the preload failed (see $LOGDIR/$name)"
    fi
    if grep -qi "pypi.org\|files.pythonhosted.org" "$LOGDIR/$name/venv.log" 2>/dev/null; then
        fail "$shape: the venv asked PyPI"
    fi
    if ! grep -q "from the payload's wheelhouse (no download)" "$LOGDIR/$name/venv.log" 2>/dev/null; then
        fail "$shape: the venv log does not say it installed from the payload's wheelhouse alone"
    fi
    [ "$fails" -ne 0 ] || log "$shape: set up and running offline, $count processes, all as $shape"
    docker stop -t 60 "$ctr" >/dev/null || true
    echo "::endgroup::"
done

# --- the image's size ------------------------------------------------------------------------
bytes="$(docker save "$IMAGE" | gzip -1 | wc -c | tr -d ' ')"
log "compressed (gzip -1 of docker save): $((bytes / 1000000)) MB"
if [ "$bytes" -gt "$MAX_COMPRESSED" ]; then fail "the image is $((bytes / 1000000)) MB compressed: over 1 GB"; fi

if [ "$fails" -ne 0 ]; then
    log "$fails check(s) failed; logs in $LOGDIR"
    exit 1
fi
log "every NAS shape passed"
