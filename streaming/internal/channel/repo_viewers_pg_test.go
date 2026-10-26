package channel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// Behind the front door (KeepViewers) an engine's runtime row leaves viewer_count
// to the front door's count: a new row starts at none, a rewrite keeps the count.
// Without it the engine's count is written, as before.
func TestPGRepoKeepViewers(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels (id, number, name, slug, mode) VALUES
		($1, 101, 'A', 'a', 'shuffle'), ($2, 102, 'B', 'b', 'shuffle')`, a, b); err != nil {
		t.Fatal(err)
	}
	viewers := func(ch uuid.UUID) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT viewer_count FROM channel_runtime WHERE channel_id = $1`, ch).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rt := func(ch uuid.UUID, n int) Runtime {
		return Runtime{ChannelID: ch, HostID: "home", State: StateLive, ViewerCount: n, StartedAt: time.Now()}
	}
	plain := NewPGRepo(pool, "mac")
	if err := plain.SetRuntime(ctx, rt(a, 2)); err != nil {
		t.Fatal(err)
	}
	if n := viewers(a); n != 2 {
		t.Fatalf("the engine's count: %d, want 2", n)
	}

	keep := NewPGRepo(pool, "mac")
	keep.KeepViewers = true
	if err := keep.SetRuntime(ctx, rt(b, 3)); err != nil {
		t.Fatal(err)
	}
	if n := viewers(b); n != 0 {
		t.Fatalf("a new row behind the front door: %d, want 0", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_runtime SET viewer_count = 7 WHERE channel_id = $1`, b); err != nil {
		t.Fatal(err)
	}
	if err := keep.SetRuntime(ctx, rt(b, 1)); err != nil {
		t.Fatal(err)
	}
	if n := viewers(b); n != 7 {
		t.Fatalf("a rewrite behind the front door: %d, want the front door's 7", n)
	}
}
