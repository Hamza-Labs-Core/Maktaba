#!/usr/bin/env bash
# The PostgreSQL 18 checks of a server payload (Epic 49, story 49.1), shared by the Mac, Linux
# and Windows payloads' check.sh: postgres-18/ is PostgreSQL 18; it starts with pg_textsearch
# preloaded, as the bundled server starts it (pgbundle); pgvector, pg_textsearch and the
# contrib extensions Maktaba uses are created, a BM25 index answers a ranked query through the
# index, the schema migrates (--api), and a PostgreSQL 16 cluster of the payload's own (--old,
# stopped) upgrades to it with pg_upgrade and keeps its data (story 49.2's step).
#
# It prints one line per check, "ok <what>" or "FAIL <what>", and last "ok PostgreSQL 18
# checks done" when it got to the end; it exits 1 when a check failed. Each check.sh shows the
# lines its own way.
#
# usage: pg18-check.sh --pg18 BIN --scratch DIR [--pg16 BIN --old DATADIR] [--api MAKTABA_API]
set -uo pipefail

pg18="" pg16="" old="" scratch="" api=""
while [ $# -gt 0 ]; do
    case "$1" in
        --pg18) pg18="$2"; shift 2 ;;
        --pg16) pg16="$2"; shift 2 ;;
        --old) old="$2"; shift 2 ;;
        --scratch) scratch="$2"; shift 2 ;;
        --api) api="$2"; shift 2 ;;
        *) echo "FAIL pg18-check.sh: unknown option $1"; exit 2 ;;
    esac
done
[ -n "$pg18" ] && [ -n "$scratch" ] || { echo "FAIL usage: pg18-check.sh --pg18 BIN --scratch DIR"; exit 2; }

fails=0
ok() { printf 'ok %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*"; fails=$((fails + 1)); }
x=""
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) x=.exe ;; esac
mkdir -p "$scratch"
port=$((55000 + RANDOM % 900))
# the flags the bundled server starts PostgreSQL 18 with that matter here (pgbundle.serverArgs)
flags="-p $port -k '' -c listen_addresses=127.0.0.1 -c wal_level=logical -c max_replication_slots=40 -c shared_preload_libraries=pg_textsearch"
psql18() { "$pg18/psql$x" -X -h 127.0.0.1 -p "$port" -U maktaba -Atq -v ON_ERROR_STOP=1 "$@"; }

ver="$("$pg18/postgres$x" --version 2>&1)"
case "$ver" in
    *" 18."*) ok "postgres --version: $ver" ;;
    *) fail "postgres-18/bin/postgres --version: $ver (18.x expected)" ;;
esac

data="$scratch/data18"
if "$pg18/initdb$x" -D "$data" -U maktaba --auth=trust -E UTF8 --locale=C >"$scratch/initdb18.log" 2>&1 &&
    "$pg18/pg_ctl$x" -D "$data" -l "$scratch/pg18.log" -w -t 60 -o "$flags" start >/dev/null 2>&1; then
    out="$(psql18 -d postgres -c "SHOW shared_preload_libraries" 2>&1)"
    case "$out" in *pg_textsearch*) ok "shared_preload_libraries: $out" ;; *) fail "shared_preload_libraries: $out" ;; esac
    out="$(psql18 -d postgres \
        -c "CREATE EXTENSION pg_trgm; CREATE EXTENSION pgcrypto; CREATE EXTENSION unaccent; CREATE EXTENSION vector;" \
        -c "CREATE EXTENSION pg_textsearch;" \
        -c "SELECT string_agg(extname || ' ' || extversion, ', ' ORDER BY extname) FROM pg_extension WHERE extname <> 'plpgsql';" 2>&1)"
    case "$out" in
        *pg_textsearch*vector*) ok "extensions: $(printf '%s\n' "$out" | tail -1)" ;;
        *) fail "CREATE EXTENSION (pg_textsearch, vector, the contrib ones): $(printf '%s\n' "$out" | tail -3)" ;;
    esac
    # Maktaba's text search configuration (unaccent in front of simple), a BM25 index on it,
    # and the ranked query of Epic 49 (ORDER BY <@> LIMIT), answered by the index
    out="$(psql18 -d postgres \
        -c "CREATE TEXT SEARCH CONFIGURATION public.maktaba_search (COPY = simple);" \
        -c "ALTER TEXT SEARCH CONFIGURATION public.maktaba_search ALTER MAPPING FOR hword, hword_part, word WITH unaccent, simple;" \
        -c "CREATE TABLE docs (id int PRIMARY KEY, body text NOT NULL);" \
        -c "INSERT INTO docs SELECT i, CASE WHEN i = 7 THEN 'الصبر الصبر الصبر عند المصيبة' ELSE 'كلمة ' || i || ' الصبر' END FROM generate_series(1, 500) i;" \
        -c "CREATE INDEX docs_bm25 ON docs USING bm25 (body) WITH (text_config = 'public.maktaba_search');" \
        -c "SET enable_seqscan = off;" \
        -c "SELECT 'top: ' || string_agg(id::text, ',') FROM (SELECT id FROM docs ORDER BY body <@> to_bm25query('الصبر المصيبة', 'docs_bm25') LIMIT 3) t;" \
        -c "EXPLAIN (COSTS OFF) SELECT id FROM docs ORDER BY body <@> to_bm25query('الصبر', 'docs_bm25') LIMIT 3;" 2>&1)"
    case "$out" in
        *"top: 7,"*docs_bm25*) ok "pg_textsearch: a BM25 index ranks the right row first ($(grep -m1 '^top: ' <<<"$out"))" ;;
        *) fail "pg_textsearch's BM25 query: $(printf '%s\n' "$out" | tail -4)" ;;
    esac
    if [ -n "$api" ]; then
        if psql18 -d postgres -c "CREATE DATABASE maktaba_check18;" >/dev/null 2>&1 &&
            out="$(cd "$scratch" && env -u MAKTABA_MIGRATIONS_DIR \
                DATABASE_URL="postgres://maktaba@127.0.0.1:$port/maktaba_check18?sslmode=disable" "$api" migrate up 2>&1)"; then
            ok "the schema migrates on PostgreSQL 18 (maktaba-api migrate up)"
        else
            fail "maktaba-api migrate up on PostgreSQL 18: $(printf '%s\n' "$out" | tail -3)"
        fi
    fi
    "$pg18/pg_ctl$x" -D "$data" -m fast -w stop >/dev/null 2>&1 || fail "pg_ctl stop (PostgreSQL 18)"
else
    fail "PostgreSQL 18 initdb/start: $(tail -3 "$scratch/initdb18.log" "$scratch/pg18.log" 2>/dev/null)"
fi

# the payload's PostgreSQL 16 cluster, upgraded: a copy (the check leaves the old one as it
# was), checksums as the old cluster has them, the old server given wal_level=logical as the
# bundled server runs it (a home's machines' slots need it to start at all)
if [ -n "$pg16" ] && [ -n "$old" ] && [ -f "$old/PG_VERSION" ]; then
    new="$scratch/upgraded"
    sums=--no-data-checksums
    if "$pg16/pg_controldata$x" "$old" 2>/dev/null | grep -Eq 'Data page checksum version: *[1-9]'; then sums=--data-checksums; fi
    work="$scratch/upgrade-work"
    mkdir -p "$work"
    # its servers' socket: a short directory (a unix socket path has at most 103 bytes);
    # Windows' pg_upgrade connects over TCP
    sock=()
    if [ -z "$x" ]; then
        sockdir="$(mktemp -d /tmp/pgu.XXXXXX)"
        sock=(-s "$sockdir")
    fi
    if "$pg18/initdb$x" -D "$new" -U maktaba --auth=trust -E UTF8 --locale=C "$sums" >"$scratch/initdb-up.log" 2>&1 &&
        (cd "$work" && "$pg18/pg_upgrade$x" -b "$pg16" -B "$pg18" -d "$old" -D "$new" -U maktaba \
            -p $((port + 1)) -P $((port + 2)) "${sock[@]}" --copy \
            -o "-c wal_level=logical -c max_replication_slots=40" -O "-c wal_level=logical -c max_replication_slots=40") \
            >"$scratch/pg_upgrade.log" 2>&1; then
        ok "pg_upgrade 16 -> 18 ($(grep -c . "$scratch/pg_upgrade.log") lines of output)"
        if "$pg18/pg_ctl$x" -D "$new" -l "$scratch/pg-up.log" -w -t 60 -o "$flags" start >/dev/null 2>&1; then
            out="$(psql18 -d postgres -c "SELECT string_agg(datname, ',' ORDER BY datname) FROM pg_database WHERE NOT datistemplate;" 2>&1)"
            case "$out" in *maktaba_check*) ok "the upgraded cluster has its databases: $out" ;; *) fail "the upgraded cluster's databases: $out" ;; esac
            "$pg18/pg_ctl$x" -D "$new" -m fast -w stop >/dev/null 2>&1 || fail "pg_ctl stop (the upgraded cluster)"
        else
            fail "the upgraded cluster does not start: $(tail -3 "$scratch/pg-up.log" 2>/dev/null)"
        fi
    else
        fail "pg_upgrade 16 -> 18: $(tail -12 "$scratch/initdb-up.log" "$scratch/pg_upgrade.log" 2>/dev/null)"
    fi
fi

[ -n "${sockdir:-}" ] && rm -rf "$sockdir"
ok "PostgreSQL 18 checks done"
[ "$fails" = 0 ]
