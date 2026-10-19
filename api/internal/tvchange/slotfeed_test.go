package tvchange

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// Story 33.13 AC1: the front door's failover and staging crash-loop events become
// notices, once each; the first run starts at the newest event; a repeat about the
// same host within the hour is dropped (EC1).
func TestNotifySlotEvents(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			ctx := context.Background()
			event := func(kind, slot, detail string) {
				t.Helper()
				if _, err := be.DB.Exec(`INSERT INTO tv_slot_events (host_id, kind, slot, detail) VALUES ($1, $2, $3, $4)`,
					"home", kind, slot, detail); err != nil {
					t.Fatal(err)
				}
			}
			tick := func() {
				t.Helper()
				if err := w.ctl.notifySlotEvents(ctx); err != nil {
					t.Fatal(err)
				}
			}
			event(SlotEventFailover, "blue", `{"to":"green"}`) // before the feed existed
			tick()
			if got := w.rec.kinds(); len(got) != 0 {
				t.Fatalf("an event older than the feed was announced: %v", got)
			}
			event(SlotEventFailover, "green", `{"to":"blue","why":"exited"}`)
			event(SlotEventCrashLoop, "amber", `{"restarts":3,"window_sec":600}`)
			tick()
			tick() // read once: the cursor moved
			got := w.rec.kinds()
			if len(got) != 2 || got[0] != NotifyFailover || got[1] != NotifyCrashLoop {
				t.Fatalf("notices %v", got)
			}
			w.rec.mu.Lock()
			fo, cl := w.rec.got[0], w.rec.got[1]
			w.rec.mu.Unlock()
			if fo.Params["slot"] != "green" || fo.Params["to"] != "blue" || fo.Params["host"] != "home" ||
				fo.Link != "/admin/live-tv" || fo.TitleKey != "notify."+NotifyFailover {
				t.Fatalf("failover notice %+v", fo)
			}
			if cl.Severity != "error" || cl.Params["slot"] != "amber" {
				t.Fatalf("crash loop notice %+v", cl)
			}
			// another failover of the same host within the hour: dropped; after it: sent
			event(SlotEventFailover, "blue", `{"to":"green"}`)
			tick()
			if n := len(w.rec.kinds()); n != 2 {
				t.Fatalf("within the hour: %d notices", n)
			}
			w.now = w.now.Add(61 * time.Minute)
			event(SlotEventFailover, "green", `{"to":"blue"}`)
			tick()
			if n := len(w.rec.kinds()); n != 3 {
				t.Fatalf("after the hour: %d notices", n)
			}
		})
	}
}
