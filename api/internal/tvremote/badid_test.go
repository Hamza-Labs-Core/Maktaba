package tvremote_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// Post-review: the remote and simulcast ids are Postgres UUIDs, so an id from
// a path or a body that is not one names no row: a 404 refusal, never the
// 500 of a failed ::uuid cast.
func checkBadIDs(t *testing.T, c *tvremote.Controller, channel string) {
	t.Helper()
	ctx := context.Background()
	const bad = "not-a-uuid"
	name, kind := "x", "rtmp"
	want404 := func(what string, err error) {
		t.Helper()
		var te *tvremote.TuneError
		if !errors.As(err, &te) || te.Status != http.StatusNotFound {
			t.Errorf("%s: %v, want a 404 refusal", what, err)
		}
	}
	_, err := c.Heartbeat(ctx, viewer("u"), bad)
	want404("heartbeat", err)
	want404("end", c.End(ctx, viewer("u"), bad))
	_, err = c.UpdateDestination(ctx, "a", bad, tvremote.DestinationInput{Name: &name, Kind: &kind})
	want404("update destination", err)
	want404("delete destination", c.DeleteDestination(ctx, "a", bad))
	_, err = c.TestDestination(ctx, bad)
	want404("test destination", err)
	want404("start output", c.StartOutput(ctx, "a", bad))
	want404("stop output", c.StopOutput(ctx, "a", bad))
	_, err = c.SimulcastStatus(ctx, bad)
	want404("channel simulcast", err)
	_, err = c.SetChannelSimulcast(ctx, "a", bad, nil)
	want404("set simulcast, bad channel", err)
	_, err = c.SetChannelSimulcast(ctx, "a", channel, []tvremote.AttachmentInput{{DestinationID: bad, Mode: "always"}})
	want404("set simulcast, bad destination", err)
	if err := c.StopPush(ctx, bad); err == nil {
		t.Error("stop push of a non-UUID id answered nil")
	}
}

func TestNonUUIDIDs(t *testing.T) {
	fx := newSim(t)
	checkBadIDs(t, fx.c, fx.chans[0])
}

// The same on real Postgres (skips without MAKTABA_TEST_PG_URL).
func TestNonUUIDIDsPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ch := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle')`, ch); err != nil {
		t.Fatal(err)
	}
	in := &tvremote.Integration{DB: db, Secrets: secrets.NewMemory(),
		Ext: &fakeExt{settings: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}}}
	checkBadIDs(t, &tvremote.Controller{DB: db, Integration: in, TuneWait: -1}, ch)
}
