//go:build integration

package broadcast

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.28, the home door end to end: a home's report counts its own rows only (another home
// of the same owner is not counted), folds a country under k into Other, and a home naming its
// sibling's id is 403.
func TestHomeAnalyticsReportIsTheHomesOwn(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	owner, err := stores.NewUsers(pool.DB).Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 2)
	for i := range ids {
		if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash)
            VALUES ($1, 'h', $2, 'x') RETURNING id`, owner.ID, "s"+uuid.NewString()[:8]).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	home, sibling := ids[0], ids[1]
	now := time.Date(2026, 11, 14, 15, 30, 0, 0, time.UTC)
	hour := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		pub       string
		mins      float64
		countries string
	}{{home, 600, `{"EG":12,"DE":2}`}, {sibling, 6000, `{"FR":40}`}} {
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO broadcast_usage_hourly
            (publisher_id, channel_id, hour, viewer_minutes, sessions, countries, devices)
            VALUES ($1, 'c1', $2, $3, 14, $4, '{"web":14}')`, row.pub, hour, row.mins, row.countries); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO broadcast_live (publisher_id, channel_id, viewers, updated_at)
        VALUES ($1, 'c1', 3, $2), ($3, 'c1', 90, $2)`, home, now, sibling); err != nil {
		t.Fatal(err)
	}
	// the Broadcaster tier sees everything (servers.broadcast_plan, read through to_jsonb)
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, home); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	MountAnalyticsHome(r, &AnalyticsDeps{DB: pool.DB, Servers: stores.NewServers(pool.DB),
		Agents: fakeAgents{home: "s3cret", sibling: "s3cret"}, Now: func() time.Time { return now }})

	if w := homeGet(r, home, "s3cret", "?server="+sibling); w.Code != http.StatusForbidden {
		t.Fatalf("the sibling's id: %d %s", w.Code, w.Body)
	}
	w := homeGet(r, home, "s3cret", "?period=today&tz=UTC")
	if w.Code != http.StatusOK {
		t.Fatalf("own report: %d %s", w.Code, w.Body)
	}
	var rep bc.Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.LiveViewers != 3 || rep.Hours.Today != 10 || rep.PeriodHours != 10 {
		t.Fatalf("the sibling's rows were counted: %+v", rep)
	}
	if rep.Level != bc.LevelFull || rep.Hours.Allowance == nil || *rep.Hours.Allowance != AllowanceBroadcaster {
		t.Fatalf("level %q allowance %v, want full and the Broadcaster allowance", rep.Level, rep.Hours.Allowance)
	}
	want := []bc.Count{{Key: "EG", Value: 12}, {Key: bc.OtherKey, Value: 2}}
	if len(rep.Countries) != 2 || rep.Countries[0] != want[0] || rep.Countries[1] != want[1] {
		t.Fatalf("countries %+v, want %+v (Germany is never named)", rep.Countries, want)
	}
}
