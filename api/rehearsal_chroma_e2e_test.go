//go:build unix

package main

// Update-rehearsal scenario 18 (Epic 43 story 43.5; docs/update-rehearsal.md): a home whose
// copy of its Chroma vectors into Postgres stopped part way updates to the release that
// retired Chroma.
//
// The releases before 43.5 kept semantic search's vectors in Chroma (<StateDir>/chroma)
// until a one-time copy moved them into Postgres (story 43.3). A home whose copy never
// finished must not embed anything again. This commit has no
// Chroma, but its copy reads Chroma's files: the update finishes the copy, the videos it
// copies keep their index fingerprints (no index job), and Chroma's files go only once
// the copy is done (the start after). The folder planted here is the one Chroma 1.5.9
// wrote (pipeline/tests/search/data/chroma-1.5.9: five transcripts of fifty passages and
// an orphan one, writes still in its queue); the copy is stopped after its first page, as
// a killed copy leaves it. Each release runs its own pipeline (as in 16.x).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// chromaFixture is the Chroma folder the pipeline's tests read (chroma_fixture.py).
var chromaFixture = filepath.Join("..", "pipeline", "tests", "search", "data", "chroma-1.5.9")

// chromaFixtureTranscripts are its transcripts (chroma_fixture.TRANSCRIPTS); its orphan
// transcript is in no database.
var chromaFixtureTranscripts = func() []string {
	out := []string{}
	for i := 1; i <= 5; i++ {
		out = append(out, fmt.Sprintf("00000000-0000-0000-0000-0000%04x%04x", 0x4305, i))
	}
	return out
}()

const chromaFixturePer = 50

// chromaRead is what the folder holds, read with this commit's own reader (the copy's).
const chromaRead = `
import json, os, sys
from pathlib import Path
from maktaba_pipeline.search.chroma_copy import ChromaFiles, collection_name
f = ChromaFiles.open(Path(sys.argv[1]), collection_name("intfloat/multilingual-e5-small"))
f.load_labels()
fd = os.open(f.segment_dir / "data_level0.bin", os.O_RDONLY)
el = f.read(fd, 0, f.elements())
os.close(fd)
live = {}
for label, vec, ok in zip(el.labels.tolist(), el.vectors, el.live.tolist()):
    eid = f.label_to_id.get(label)
    if eid is not None and ok:
        live[eid] = vec
for _seq, op, eid, vec in f.queue_tail():
    if op == 3:
        live.pop(eid, None)
    elif vec is not None:
        live[eid] = vec
print(json.dumps({k: [float(x) for x in v] for k, v in live.items()}))
`

// chromaStopAfterAPage runs this commit's copy and stops it as a kill would, after two
// pages of the load (fifty elements each: the orphan transcript's, written as nothing,
// then the first transcript's): the queue's writes applied, one transcript copied, the
// cursor saved, the HNSW index dropped for the load.
const chromaStopAfterAPage = `
import asyncio, sys
from pathlib import Path
import asyncpg
from maktaba_pipeline.search import chroma_copy as cc

class Stop(Exception):
    pass

async def main(dsn, root):
    conn = await asyncpg.connect(dsn)
    real = cc._write_page
    pages = {"n": 0}
    async def stops(conn, model, keys, vectors, *, replace=False):
        if keys and not replace:
            pages["n"] += 1
            if pages["n"] == 3:
                raise Stop()
        return await real(conn, model, keys, vectors, replace=replace)
    cc._write_page = stops
    cfg = cc.CopyConfig(chroma_dir=Path(root), model="intfloat/multilingual-e5-small", page=50,
                        samples=50, min_free_bytes=0, build_mem="64MB", progress_every=0.0)
    try:
        await cc.copy_chroma(conn, cfg)
    except Stop:
        pass
    print(await conn.fetchval("SELECT copy_cursor FROM semantic_store_state"))
    await conn.close()

asyncio.run(main(sys.argv[1], sys.argv[2]))
`

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// plantChromaHome gives the home the fixture's transcripts (active, on five of its videos,
// each with a done index job, as a home that indexed them into Chroma has) and the folder.
func plantChromaHome(t *testing.T, db *sql.DB, chroma string) []string {
	t.Helper()
	if err := copyDir(chromaFixture, chroma); err != nil {
		t.Fatalf("the Chroma folder: %v", err)
	}
	var next int64
	if err := db.QueryRow(`SELECT COALESCE(max(id), 0) + 1 FROM transcript_segments`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	videos := []string{}
	for i, tid := range chromaFixtureTranscripts {
		var vid string
		var track int64
		if err := db.QueryRow(`SELECT v.id::text, a.id FROM videos v JOIN audio_tracks a ON a.video_id = v.id
			ORDER BY v.id OFFSET $1 LIMIT 1`, i).Scan(&vid, &track); err != nil {
			t.Fatalf("a seeded video: %v", err)
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE transcripts SET is_active = false WHERE video_id = $1`, []any{vid}},
			{`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, word_level, is_active)
				VALUES ($1, $2, $3, 'ar', 'whisper', 'large-v3', true, true)`, []any{tid, vid, track}},
			{`INSERT INTO transcript_segments (id, transcript_id, seq, start_sec, end_sec, text, confidence)
				SELECT $2 + s, $1, s, s * 6.5, s * 6.5 + 6, 'chroma passage ' || s, 0.9 FROM generate_series(0, $3 - 1) s`,
				[]any{tid, next, chromaFixturePer}},
			{`SELECT processing_jobs_enqueue('video', $1::uuid, 'index', 100, true, false,
				'{"why":"rehearsal","by":"system"}'::jsonb, NULL, NULL, NULL)`, []any{vid}},
			{`UPDATE processing_jobs SET state = 'done', outcome = 'changed', finished_at = now()
				WHERE subject_kind = 'video' AND subject_id = $1::uuid AND stage = 'index' AND state <> 'done'`, []any{vid}},
		} {
			if _, err := db.Exec(q.sql, q.args...); err != nil {
				t.Fatalf("%s: %v", strings.Fields(q.sql)[0], err)
			}
		}
		next += chromaFixturePer
		videos = append(videos, vid)
	}
	return videos
}

// pgVectors are the copied vectors of the fixture's transcripts, by Chroma id.
func pgVectors(t *testing.T, db *sql.DB) map[string][]float64 {
	t.Helper()
	rows, err := db.Query(`SELECT s.transcript_id::text || ':' || s.seq, e.embedding::text FROM segment_embeddings e
		JOIN transcript_segments s ON s.id = e.segment_id WHERE s.transcript_id::text = ANY($1::text[])`,
		"{"+strings.Join(chromaFixtureTranscripts, ",")+"}")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]float64{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		var v []float64
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		out[id] = v
	}
	return out
}

func TestUpdateRehearsal18ChromaCopyFinished(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	r.realPipeline(nil)
	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	defer db.Close()
	state := home.root // the state dir: data_dir on Linux (config.StateDir)
	chroma := filepath.Join(state, "chroma")
	done := filepath.Join(state, "chroma-copy.done")
	py := filepath.Join(k.venvs(), k.cur, "bin", "python")

	// 1. a home that indexed into Chroma, whose copy stopped after a page (planted while
	// the previous release serves: its own copy runs only at a start)
	videos := plantChromaHome(t, db, chroma)
	if _, err := db.Exec(`UPDATE semantic_store_state SET store = 'chroma', copy_cursor = NULL, copied_rows = 0,
		copy_done_at = NULL`); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-c", chromaRead, chroma).Output()
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var all map[string][]float64
	if err := json.Unmarshal(out, &all); err != nil {
		t.Fatal(err)
	}
	want := map[string][]float64{}
	for id, v := range all {
		tid, _, _ := strings.Cut(id, ":")
		for _, ft := range chromaFixtureTranscripts {
			if tid == ft {
				want[id] = v // not the orphan transcript's: in no database
			}
		}
	}
	stopped, err := exec.Command(py, "-c", chromaStopAfterAPage, home.dsn("maktaba_admin", "maktaba"), chroma).CombinedOutput()
	if err != nil {
		t.Fatalf("the copy stopped part way: %v\n%s", err, stopped)
	}
	before := len(pgVectors(t, db))
	if before == 0 || before >= len(want) {
		t.Fatalf("the part-done copy has %d of %d vectors: not part way (%s)", before, len(want), stopped)
	}
	var lastJob int64
	_ = db.QueryRow(`SELECT COALESCE(max(id), 0) FROM processing_jobs`).Scan(&lastJob)
	r.logf("18: a copy stopped part way: %d of %d vectors copied (cursor %s)", before, len(want),
		strings.TrimSpace(string(stopped)))
	counts := seedCounts(t, db)
	delete(counts, "segment_embeddings") // the copy adds the rest
	delete(counts, "transcripts")
	delete(counts, "transcript_segments")

	// 2. the update: done, no restore; the copy finishes, the folder stays meanwhile
	run, mon, states := r.update(home, k.curSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "18: an update of a home whose Chroma copy stopped part way", 2*time.Minute)
	home.assertServing(k.cur)
	r.noRestore(home, states)
	waitFor(t, "the copy finished", 10*time.Minute, func() bool {
		var store string
		var doneAt sql.NullTime
		_ = db.QueryRow(`SELECT store, copy_done_at FROM semantic_store_state`).Scan(&store, &doneAt)
		if store == "postgres" && doneAt.Valid {
			return true
		}
		if !pathExists(filepath.Join(chroma, "chroma.sqlite3")) {
			t.Fatal("Chroma's files went before the copy was done")
		}
		return false
	})
	home.assertNoCrashLoop("18: after the copy")
	if !pathExists(done) || !pathExists(filepath.Join(chroma, "chroma.sqlite3")) {
		t.Errorf("after the copy: done file %v, folder %v (want both: the folder goes the start after)",
			pathExists(done), pathExists(filepath.Join(chroma, "chroma.sqlite3")))
	}

	// every vector Chroma held is in Postgres, as Chroma held it
	got := pgVectors(t, db)
	if len(got) != len(want) {
		t.Errorf("%d vectors in Postgres, Chroma held %d", len(got), len(want))
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok || len(g) != len(w) {
			t.Errorf("%s was not copied whole", id)
			continue
		}
		for i := range w {
			if math.Abs(g[i]-w[i]) > 2e-3 { // halfvec
				t.Errorf("%s differs at %d: %f, Chroma %f", id, i, g[i], w[i])
				break
			}
		}
	}
	// nothing was embedded again: no index job for those videos since the update, once
	// the reconciler (every 60 s) had a pass or two
	time.Sleep(75 * time.Second)
	var jobs int
	_ = db.QueryRow(`SELECT count(*) FROM processing_jobs WHERE stage = 'index' AND id > $1
		AND subject_id::text = ANY($2::text[])`, lastJob, "{"+strings.Join(videos, ",")+"}").Scan(&jobs)
	if jobs != 0 {
		t.Errorf("%d index jobs for the copied videos since the update: something embeds them again", jobs)
	}
	after := seedCounts(t, db)
	delete(after, "segment_embeddings")
	delete(after, "transcripts")
	delete(after, "transcript_segments")
	r.countsUnchanged(counts, after)

	// 3. the start after the copy: Chroma's files go, the vectors stay
	home.stop()
	home.start()
	home.waitServing(k.cur, 5*time.Minute)
	waitFor(t, "the Chroma folder gone the start after the copy", 2*time.Minute,
		func() bool { return !pathExists(chroma) })
	home.assertNoCrashLoop("18: the start after the copy")
	home.assertServing(k.cur)
	if n := len(pgVectors(t, db)); n != len(want) {
		t.Errorf("after the folder went: %d vectors, want %d", n, len(want))
	}
	r.summary(fmt.Sprintf("18 a part-done Chroma copy finishes at the update: %d -> %d vectors, no index job, "+
		"Chroma's files gone the start after", before, len(want)))
}
