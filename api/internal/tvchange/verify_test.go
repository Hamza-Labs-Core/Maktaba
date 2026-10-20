package tvchange

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// blockResult runs the database verification of a regenerated channel whose new
// generation (1) is edited by edit and whose boundary is boundary, and returns the
// channel.block check.
func blockResult(t *testing.T, w *svWorld, boundary time.Time, edit func()) Check {
	t.Helper()
	edit()
	c := w.staged("v2")
	w.exec(`UPDATE tv_release_channels SET sched_changed = $1, boundary_at = $2 WHERE release_id = $3`,
		true, w.ts(boundary), *w.get(c.ID).ReleaseID)
	report, _, _, err := w.ctl.Verify(context.Background(), w.get(c.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range report["checks"].([]Check) {
		if k.Check == "channel.block" {
			return k
		}
	}
	t.Fatalf("no channel.block in %v", report)
	return Check{}
}

// The live programme 20:00-21:00 is on the air at 20:10 (newSVWorld's now).
func TestVerifyBlockOnTheAir(t *testing.T) {
	blockStart := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		boundary time.Time
		edit     func(w *svWorld)
		want     string
		value    string
	}{
		{"kept", blockStart.Add(time.Hour), func(*svWorld) {}, ResultPass, "is kept"},
		{
			// boundary_max_wait_minutes: the generation ends the programme at the cap
			"capped at boundary_max_wait", blockStart.Add(40 * time.Minute), func(w *svWorld) {
				w.exec(`UPDATE channel_programs SET end_at = $1 WHERE channel_id = $2 AND sched_gen = 1 AND seq = 1`,
					w.ts(blockStart.Add(40*time.Minute)), w.ch)
			}, ResultWarn, "ends early",
		},
		{
			// ends early but not at the channel's boundary: a real cut
			"cut elsewhere", blockStart.Add(time.Hour), func(w *svWorld) {
				w.exec(`UPDATE channel_programs SET end_at = $1 WHERE channel_id = $2 AND sched_gen = 1 AND seq = 1`,
					w.ts(blockStart.Add(30*time.Minute)), w.ch)
			}, ResultFail, "would cut",
		},
		{
			// another programme at now in the new generation
			"replaced", blockStart.Add(time.Hour), func(w *svWorld) {
				w.exec(`UPDATE channel_programs SET start_at = $1 WHERE channel_id = $2 AND sched_gen = 1 AND seq = 1`,
					w.ts(blockStart.Add(5*time.Minute)), w.ch)
			}, ResultFail, "would cut",
		},
		{
			// verified after the planned swap: the boundary is behind now, the swap
			// re-anchors (generation 1 regenerated from 20:05 already differs)
			"boundary passed", blockStart.Add(5 * time.Minute), func(w *svWorld) {
				w.exec(`UPDATE channel_programs SET start_at = $1 WHERE channel_id = $2 AND sched_gen = 1 AND seq = 1`,
					w.ts(blockStart.Add(5*time.Minute)), w.ch)
			}, ResultPass, "re-anchors",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, be := range tvchangetest.Backends(t) {
				w := newSVWorld(t, be.DB)
				k := blockResult(t, w, tc.boundary, func() { tc.edit(w) })
				if k.Result != tc.want || !strings.Contains(k.Value, tc.value) {
					t.Fatalf("%s: got %s %q, want %s ~%q", be.Name, k.Result, k.Value, tc.want, tc.value)
				}
			}
		})
	}
}
