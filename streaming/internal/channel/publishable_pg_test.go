package channel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// Story 50.23 T04: a published channel's block whose item loses its publishable mark
// after it was scheduled (here, its source's permission is withdrawn) reads as
// NotPublishable, so the slot plays the slate; unpublished, or with publishable_only
// off, it plays as scheduled.
func TestPGRepo_LostMarkSlates(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	lib, ch, vid := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'Reciter A', '{/m}')`, lib)
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 9, 'Quran', 'quran', 'shuffle')`, ch)
	exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
		VALUES ($1, $2, '/m/a.mp4', 'a.mp4', encode(sha256('a'::bytea), 'hex'), 1, now(), 'ready')`, vid, lib)
	exec(`INSERT INTO publishable_sources (kind, ref, proof_url, recorded_by) VALUES ('speaker', 'Reciter A', 'https://r.test/ok', 'A')`)
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	exec(`INSERT INTO channel_programs (channel_id, seq, kind, video_id, start_at, end_at, source_duration)
		VALUES ($1, 1, 'program', $2, $3, $4, 3600000)`, ch, vid, at, at.Add(time.Hour))
	exec(`INSERT INTO broadcast_channels (channel_id, slug, state) VALUES ($1, 'quran', 'live')`, ch)

	repo := NewPGRepo(pool, "home")
	lost := func() bool {
		t.Helper()
		got, err := repo.ProgramsFrom(ctx, ch, time.Now(), 5)
		if err != nil || len(got) != 1 {
			t.Fatalf("programs %+v %v", got, err)
		}
		return got[0].NotPublishable
	}
	if lost() {
		t.Fatal("a permitted recitation reads as not publishable")
	}
	exec(`UPDATE publishable_sources SET revoked_at = now()`)
	if !lost() {
		t.Fatal("the withdrawn permission's item still airs")
	}
	exec(`INSERT INTO app_settings (key, value) VALUES ('broadcast.publishable_only', 'false'::jsonb)`)
	if lost() {
		t.Fatal("publishable_only off still slates")
	}
	exec(`DELETE FROM app_settings WHERE key = 'broadcast.publishable_only'`)
	exec(`UPDATE broadcast_channels SET state = 'draft'`)
	if lost() {
		t.Fatal("an unpublished channel still slates")
	}
}
