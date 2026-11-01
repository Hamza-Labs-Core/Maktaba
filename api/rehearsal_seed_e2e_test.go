//go:build unix

package main

// The update rehearsal's seeded home (docs/update-rehearsal.md): the previous release
// installed as the Mac app, its database made by that release, then filled by the seed
// below to a realistic shape. Seeding the full profile takes minutes (two million
// vectors and their HNSW index), so the CI job caches the whole installed machine per
// previous release, profile and seed version (TestUpdateRehearsalSeed makes it).
//
// Profiles (MAKTABA_REHEARSAL_PROFILE):
//
//	full     150,000 videos with their probe, audio tracks and transcripts, 2,000,000 transcript
//	         segments each with its halfvec(384) embedding (the HNSW index built over
//	         them), 2,000 series with 40,000 episodes, 600 collections with 60,000 items
//	restore  the same shape with 300,000 segments and vectors: the scenarios that put a
//	         dump back (a parallel restore, chunked vectors, the HNSW index rebuilt)
//	small    5,000 videos, 20,000 segments: the scenarios that never restore
//
// Every profile also has three approved full machines (their logins in the workers'
// group, maktaba_worker), and one machine's copy granted, which makes the cluster copy's
// publication and grants every table to the replicas' group (maktaba_replicas), as on the
// owner's home.

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedVersion changes whenever the seed below does: the cached seeds are keyed on it.
const seedVersion = "5"

type seedProfile struct {
	Name     string
	Videos   int
	Segments int
}

var seedProfiles = map[string]seedProfile{
	"full":    {"full", 150_000, 2_000_000},
	"restore": {"restore", 150_000, 300_000},
	"small":   {"small", 5_000, 20_000},
}

// seedMachine is one of the seeded home's fake machines: its id and login.
type seedMachine struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// seedInfo is seed.json, beside the seeded machine.
type seedInfo struct {
	Profile  string           `json:"profile"`
	Version  string           `json:"seed_version"`
	Prev     string           `json:"prev"`
	Counts   map[string]int64 `json:"counts"`
	Machines []seedMachine    `json:"machines"`
	Took     string           `json:"took"`
}

// seedsDir is where seeded machines are kept (the CI job's cache).
func seedsDir(r *rehearsal) string {
	if d := os.Getenv("MAKTABA_REHEARSAL_SEEDS"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(r.work), "seeds")
}

// seedKey names a seeded machine: the previous release, the profile, the seed's version.
func seedKey(r *rehearsal, profile string) string {
	return fmt.Sprintf("%s-%s-v%s", r.kit.prev, profile, seedVersion)
}

// TestUpdateRehearsalSeed makes the seeded home of MAKTABA_REHEARSAL_PROFILE (default
// full) in MAKTABA_REHEARSAL_SEEDS, unless it is there.
func TestUpdateRehearsalSeed(t *testing.T) {
	rehearsalOn(t)
	profile := os.Getenv("MAKTABA_REHEARSAL_PROFILE")
	if profile == "" {
		profile = "full"
	}
	r := newRehearsal(t)
	dir := filepath.Join(seedsDir(r), seedKey(r, profile))
	if info, ok := readSeed(dir); ok {
		t.Logf("the %s seed is there already (%v)", profile, info.Counts)
		return
	}
	r.makeSeed(profile, dir)
}

func readSeed(dir string) (seedInfo, bool) {
	var info seedInfo
	b, err := os.ReadFile(filepath.Join(dir, "seed.json"))
	if err != nil || json.Unmarshal(b, &info) != nil || info.Version != seedVersion {
		return info, false
	}
	return info, true
}

// seeded is a machine of its own copied from the profile's seeded home (made now when
// there is none), and what the seed holds.
func (r *rehearsal) seeded(name, profile string) (*appMachine, seedInfo) {
	r.t.Helper()
	dir := filepath.Join(seedsDir(r), seedKey(r, profile))
	info, ok := readSeed(dir)
	if !ok {
		r.logf("no %s seed in %s: making it now", profile, dir)
		r.makeSeed(profile, dir)
		if info, ok = readSeed(dir); !ok {
			r.t.Fatal("the seed was not made")
		}
	}
	m := r.machineFrom(name, filepath.Join(dir, "machine"))
	return m, info
}

// makeSeed installs the previous release as the app, lets it make its database, adds the
// owner, fills the database and stops it; seed.json last.
func (r *rehearsal) makeSeed(profile, dir string) {
	t := r.t
	t.Helper()
	p, ok := seedProfiles[profile]
	if !ok {
		t.Fatalf("no seed profile %q", profile)
	}
	start := time.Now()
	_ = os.RemoveAll(dir)
	m := r.machinePaths("seed-"+profile, filepath.Join(dir, "machine"))
	if err := os.MkdirAll(filepath.Join(m.app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(m.app, "Contents", "MacOS", "Maktaba"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	r.kit.makePayload(t, m.payload, payloadSpec{variant: "prev", version: r.kit.prev})
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		t.Fatal(err)
	}
	m.start()
	m.waitServing(r.kit.prev, 6*time.Minute)
	m.addOwner()
	db := m.superDB("maktaba")
	machines := seedDatabase(t, db, p, r.logf)
	counts := seedCounts(t, db)
	m.stop()
	_ = db.Close()
	// the seed's own logs go: each scenario's logs start with its own run
	_ = os.RemoveAll(filepath.Join(m.root, "logs"))
	info := seedInfo{Profile: profile, Version: seedVersion, Prev: r.kit.prev, Counts: counts, Machines: machines,
		Took: time.Since(start).Round(time.Second).String()}
	raw, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r.logf("the %s seed is made in %s: %v", profile, info.Took, counts)
}

// seedTables are the tables the seed fills (their rows are counted before and after).
var seedTables = []string{"libraries", "videos", "media_info", "audio_tracks", "transcripts", "transcript_segments",
	"segment_embeddings", "series", "series_episodes", "collections", "collection_items", "users", "cluster_nodes"}

func seedCounts(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, tb := range seedTables {
		out[tb] = queryInt(t, db, "SELECT count(*) FROM "+tb)
	}
	return out
}

// seedDatabase fills the library database (as the bundled Postgres's superuser). The rows
// are made by the server itself (generate_series), without triggers or key checks (they
// are consistent by construction); the big tables' secondary indexes are dropped first and
// made again after, from their own definitions, so the schema is the release's exactly.
func seedDatabase(t *testing.T, db *sql.DB, p seedProfile, logf func(string, ...any)) []seedMachine {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(what, q string, args ...any) {
		t.Helper()
		start := time.Now()
		if _, err := conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("seed: %s: %v", what, err)
		}
		logf("seed: %s (%s)", what, time.Since(start).Round(100*time.Millisecond))
	}
	exec("session", `SET session_replication_role = replica; SET synchronous_commit = off;
		SET maintenance_work_mem = '4GB'; SET max_parallel_maintenance_workers = 3; SET statement_timeout = 0`)
	// the semantic store (pgvector's extension is the superuser's to make; the table the
	// app role's, as its own function makes it)
	exec("the semantic store", `CREATE EXTENSION IF NOT EXISTS vector; SET ROLE maktaba;
		SELECT ensure_segment_embeddings(); RESET ROLE`)
	// the big tables' secondary indexes, made again after the rows
	var defs []string
	rows, err := conn.QueryContext(t.Context(), `SELECT indexrelid::regclass::text, pg_get_indexdef(indexrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid
		WHERE c.relname IN ('transcript_segments', 'segment_embeddings', 'videos') AND NOT i.indisprimary
		  AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid)`)
	if err != nil {
		t.Fatal(err)
	}
	var drops []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		drops = append(drops, name)
		defs = append(defs, def)
	}
	rows.Close()
	exec(fmt.Sprintf("drop %d secondary indexes for the load", len(drops)), "DROP INDEX "+strings.Join(drops, ", "))

	v, s := p.Videos, p.Segments
	exec("libraries", `INSERT INTO libraries (id, name, roots)
		SELECT md5('library' || g)::uuid, 'Library ' || g, ARRAY['/media/library' || g] FROM generate_series(1, 3) g`)
	exec(fmt.Sprintf("%d videos", v), `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime,
			state, title, description, duration_sec, metadata, detected_language, created_at, updated_at, last_seen_at)
		SELECT md5('video' || g)::uuid, md5('library' || (1 + g % 3))::uuid, encode(sha256(('video' || g)::bytea), 'hex'),
			'/media/library' || (1 + g % 3) || '/show ' || (g / 40) || '/episode ' || g || '.mkv', 'episode ' || g || '.mkv',
			200000000 + g * 977, now() - make_interval(mins => g), 'ready',
			'Episode ' || g || ' ' || substr(md5(g::text), 1, 8), 'The story of episode ' || g || ', told again.',
			900 + g % 3600, jsonb_build_object('codec', 'h264', 'height', 1080, 'audio', jsonb_build_array('ar', 'en')),
			CASE WHEN g % 3 = 0 THEN 'en' ELSE 'ar' END, now() - make_interval(mins => g), now(), now()
		FROM generate_series(1, $1) g`, v)
	// every video probed (ffprobe's output as the probe stores it); two in nine have the
	// bare track 0 transcribe wrote before the probe ran (no codec, channels or sample
	// rate), the owner's home's share on 2026-10-31, so slot 0171's backfill runs at scale
	exec("media info", `INSERT INTO media_info (video_id, container, video_codec, width, height, fps, bitrate_kbps,
			has_subtitles, raw_ffprobe)
		SELECT md5('video' || g)::uuid, 'matroska,webm', 'h264', 1920, 1080, 25, 4000, false,
			jsonb_build_object('format', jsonb_build_object('format_name', 'matroska,webm', 'duration', (900 + g % 3600)::text),
				'streams', jsonb_build_array(
					jsonb_build_object('index', 0, 'codec_type', 'video', 'codec_name', 'h264', 'width', 1920, 'height', 1080,
						'pix_fmt', 'yuv420p', 'disposition', jsonb_build_object('default', 1, 'attached_pic', 0)),
					jsonb_build_object('index', 1, 'codec_type', 'audio', 'codec_name', CASE WHEN g % 2 = 0 THEN 'aac' ELSE 'opus' END,
						'channels', 2, 'sample_rate', '48000', 'disposition', jsonb_build_object('default', 1),
						'tags', jsonb_build_object('language', CASE WHEN g % 3 = 0 THEN 'eng' ELSE 'ara' END))))
		FROM generate_series(1, $1) g`, v)
	exec("audio tracks", `INSERT INTO audio_tracks (id, video_id, track_index, codec, channels, sample_rate, language, is_default)
		SELECT g, md5('video' || g)::uuid, 0,
			CASE WHEN g % 9 < 2 THEN NULL WHEN g % 2 = 0 THEN 'aac' ELSE 'opus' END,
			CASE WHEN g % 9 < 2 THEN NULL ELSE 2 END, CASE WHEN g % 9 < 2 THEN NULL ELSE 48000 END,
			CASE WHEN g % 9 < 2 THEN 'und' WHEN g % 3 = 0 THEN 'eng' ELSE 'ara' END, true
		FROM generate_series(1, $1) g`, v)
	exec("transcripts", `INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, word_level)
		SELECT md5('transcript' || g)::uuid, md5('video' || g)::uuid, g, CASE WHEN g % 3 = 0 THEN 'en' ELSE 'ar' END,
			'whisper', 'large-v3', true
		FROM generate_series(1, $1) g`, v)
	// segment g belongs to video 1 + (g - 1) % videos, at seq (g - 1) / videos
	exec(fmt.Sprintf("%d transcript segments", s), `INSERT INTO transcript_segments (id, transcript_id, seq, start_sec, end_sec, text, confidence)
		SELECT g, md5('transcript' || (1 + (g - 1) % $2))::uuid, (g - 1) / $2, ((g - 1) / $2) * 6.5, ((g - 1) / $2) * 6.5 + 6,
			(ARRAY['the','library','of','a','city','where','every','story','is','kept','and','read','again','by','night',
			       'كتاب','مكتبة','قصة','ليل','مدينة','نور','بحر','صوت','حكاية','زمن'])[1 + g % 25] || ' ' ||
			(ARRAY['old','new','long','short','quiet','bright','far','near','lost','found'])[1 + (g / 7) % 10] || ' ' ||
			'segment ' || g || ' ' || substr(md5(g::text), 1, 12), 0.9
		FROM generate_series(1, $1) g`, s, v)
	// the vectors: the sum of one of 1009 random vectors and one of 997 (about a million
	// distinct vectors, cheap to make: 384 random numbers per row took most of the seed)
	exec("vector pools", `CREATE TEMP TABLE pool_a AS SELECT i,
			(SELECT array_agg(random()::real - 0.5) FROM generate_series(1, 384) WHERE i >= 0)::halfvec(384) AS v
			FROM generate_series(0, 1008) i;
		CREATE TEMP TABLE pool_b AS SELECT i,
			(SELECT array_agg(random()::real - 0.5) FROM generate_series(1, 384) WHERE i >= 0)::halfvec(384) AS v
			FROM generate_series(0, 996) i`)
	exec(fmt.Sprintf("%d vectors (halfvec 384)", s), `INSERT INTO segment_embeddings (segment_id, video_id, library_id, language, model, embedding)
		SELECT g, md5('video' || (1 + (g - 1) % $2))::uuid, md5('library' || (1 + ((g - 1) % $2 + 1) % 3))::uuid,
			CASE WHEN ((g - 1) % $2 + 1) % 3 = 0 THEN 'en' ELSE 'ar' END, 'multilingual-e5-small', a.v + b.v
		FROM generate_series(1, $1) g JOIN pool_a a ON a.i = g % 1009 JOIN pool_b b ON b.i = g % 997`, s, v)
	exec("sequences", `SELECT setval('transcript_segments_id_seq', $1)`, s)
	exec("sequences", `SELECT setval('audio_tracks_id_seq', $1)`, v)
	exec("series", `INSERT INTO series (id, library_id, name, year, numbering)
		SELECT md5('series' || g)::uuid, md5('library' || (1 + g % 3))::uuid, 'Series ' || g, 1990 + g % 35, 'season'
		FROM generate_series(1, 2000) g`)
	exec("episodes", `INSERT INTO series_episodes (series_id, video_id, season, episode)
		SELECT md5('series' || (1 + g % 2000))::uuid, md5('video' || g)::uuid, 1 + (g / 2000) % 5, 1 + g / 10000
		FROM generate_series(1, least(40000, $1)) g`, v)
	exec("collections", `INSERT INTO collections (id, library_id, name, description)
		SELECT md5('collection' || g)::uuid, md5('library' || (1 + g % 3))::uuid, 'Collection ' || g, 'Kept together: ' || g
		FROM generate_series(1, 600) g`)
	exec("collection items", `INSERT INTO collection_items (collection_id, video_id, position)
		SELECT md5('collection' || (1 + g % 600))::uuid, md5('video' || (1 + (g * 7) % $1))::uuid, g / 600
		FROM generate_series(1, 60000) g ON CONFLICT DO NOTHING`, v)
	for i, def := range defs {
		exec("index "+drops[i], def)
	}
	// three approved full machines, their logins in the workers' group (as the home makes
	// them when it approves a machine), and the first one's copy granted: the cluster
	// copy's publication, every table granted to the replicas' group
	var machines []seedMachine
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("5eed0000-0000-4000-8000-%012d", i)
		m := seedMachine{ID: id, Role: "maktaba_node_" + strings.ReplaceAll(id, "-", ""), Password: fmt.Sprintf("machine-pass-%d-0123456789", i)}
		pub := make([]byte, 32)
		_, _ = rand.Read(pub)
		exec("machine "+m.Role, fmt.Sprintf(`SET password_encryption = 'scram-sha-256';
			CREATE ROLE %s LOGIN PASSWORD '%s' IN ROLE maktaba_worker`, m.Role, m.Password))
		exec("machine row "+m.Role, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at,
				approved_by, full_node, full_capable, full_allowed, web_url)
			VALUES ($1, $2, 'approved', 'linux', 'amd64', $3, $4, now(), 'rehearsal', true, true, true, $5)`,
			id, fmt.Sprintf("rehearsal-machine-%d", i), base64.StdEncoding.EncodeToString(pub), m.Role,
			fmt.Sprintf("http://127.0.0.1:%d", 1+i))
		machines = append(machines, m)
	}
	var verifier string
	if err := conn.QueryRowContext(t.Context(), `SELECT rolpassword FROM pg_authid WHERE rolname = $1`, machines[0].Role).Scan(&verifier); err != nil {
		t.Fatal(err)
	}
	exec("triggers on", `SET session_replication_role = origin`)
	exec("the first machine's copy (publication, the replicas' grants)", `SELECT cluster_node_replica_grant($1, $2, false)`,
		machines[0].ID, verifier)
	exec("analyze", `RESET session_replication_role; ANALYZE`)
	exec("checkpoint", `CHECKPOINT`)
	return machines
}
