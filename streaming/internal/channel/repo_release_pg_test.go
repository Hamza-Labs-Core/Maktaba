package channel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// Story 33.2 TC3 (TestSlotRepoReadsRelease) and story 33.3 TC5: a slot bound to a
// release reads that release's definitions and generation, whatever channels says
// now; the live repo reads the live generation only.
func TestSlotRepoReadsRelease(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ch := uuid.MustParse("6f1c9a2e-0000-4000-8000-0000000000e1")
	exec(`INSERT INTO channels (id, number, name, slug, mode, logo_path) VALUES ($1, 101, 'Live name', 'one', 'shuffle', '/live.png')`, ch)
	var rel uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tv_releases (digest, sched_gen) VALUES ('d', 7) RETURNING id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO tv_release_channels (release_id, channel_id, def) VALUES ($1, $2,
		'{"name":"Release name","logo_path":"/release.png","enabled":true,"mode_config":{"look":{}}}')`, rel, ch)
	now := time.Now().UTC().Truncate(time.Second)
	for _, g := range []int{0, 7} {
		exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration, sched_gen)
			VALUES ($1, $2, 'slate', $3, $4, 600000, $2)`, ch, g, now.Add(-time.Minute), now.Add(time.Duration(g+1)*time.Minute))
	}

	slot, err := NewReleaseRepo(ctx, pool, "home", rel)
	if err != nil {
		t.Fatal(err)
	}
	// channels changes after the slot started: the slot is unaffected
	exec(`UPDATE channels SET name = 'Edited', logo_path = '/edited.png' WHERE id = $1`, ch)
	look, err := slot.Look(ctx, ch)
	if err != nil || look.Name != "Release name" || look.LogoPath != "/release.png" {
		t.Fatalf("slot look %+v %v", look, err)
	}
	progs, err := slot.ProgramsFrom(ctx, ch, now, 10)
	if err != nil || len(progs) != 1 || progs[0].Seq != 7 {
		t.Fatalf("slot programmes %+v %v", progs, err)
	}
	live := NewPGRepo(pool, "home")
	progs, err = live.ProgramsFrom(ctx, ch, now, 10)
	if err != nil || len(progs) != 1 || progs[0].Seq != 0 {
		t.Fatalf("live programmes (generation 0) %+v %v", progs, err)
	}
	exec(`INSERT INTO tv_routing (scope, live_gen) VALUES ('cluster', 7)`)
	progs, _ = live.ProgramsFrom(ctx, ch, now, 10)
	if len(progs) != 1 || progs[0].Seq != 7 {
		t.Fatalf("live programmes after the flip %+v", progs)
	}
	if _, err := slot.Look(ctx, uuid.New()); err != ErrChannelNotFound {
		t.Fatalf("a channel the release lacks: %v", err)
	}
	if string(slot.Settings("tv")) != "{}" {
		t.Fatal("settings")
	}
	if _, err := NewReleaseRepo(ctx, pool, "home", uuid.New()); err == nil {
		t.Fatal("an unknown release")
	}
	// a staged channel never streams from the live repo
	exec(`UPDATE channels SET lifecycle = 'staged' WHERE id = $1`, ch)
	if _, err := live.Look(ctx, ch); err != ErrChannelNotFound {
		t.Fatalf("staged channel look: %v", err)
	}
}
