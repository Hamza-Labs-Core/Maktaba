package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// fakeCloud is Maktaba Cloud as the relay agent answers it.
type fakeCloud struct {
	mu          sync.Mutex
	plan        Plan
	down        bool
	published   []PublishRequest
	unpublished []string
	tokens      int
	ingestURL   string
	n           int
}

func (f *fakeCloud) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

func (f *fakeCloud) State(context.Context) (Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.down {
		return Plan{}, ErrUnreachable
	}
	return f.plan, nil
}

func (f *fakeCloud) Publish(_ context.Context, req PublishRequest) (CloudChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.down {
		return CloudChannel{}, ErrUnreachable
	}
	f.published = append(f.published, req)
	return CloudChannel{ID: "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60", Slug: req.Slug}, nil
}

func (f *fakeCloud) Unpublish(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.down {
		return ErrUnreachable
	}
	f.unpublished = append(f.unpublished, id)
	return nil
}

func (f *fakeCloud) IngestToken(_ context.Context, ch string, rungs []string) (IngestToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.down {
		return IngestToken{}, ErrUnreachable
	}
	f.tokens++
	url := f.ingestURL
	if url == "" {
		url = "http://ingest.invalid"
	}
	return IngestToken{Token: "mkbi1.tok", Channel: "edge-" + ch[:8], ExpiresAt: time.Now().Add(24 * time.Hour),
		RefreshAfter: 3600, IngestURL: url, Rungs: rungs, MaxTopKbps: 10000}, nil
}

func (f *fakeCloud) setDown(d bool) { f.mu.Lock(); f.down = d; f.mu.Unlock() }

var broadcasterPlan = Plan{Tier: "broadcaster", MaxChannels: 3, MaxRungs: 3, MaxTopKbps: 10000,
	Access: []string{"invite", "open"}, AgreementAccepted: true, AgreementVersion: 2}

// homeWithChannel is a scratch home with one channel scheduled for the next 24 h (80 % of
// it, all the admin's own recordings) and an admin.
func homeWithChannel(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	var ch, admin string
	if err := db.QueryRowContext(ctx, `INSERT INTO channels (number, name, slug, mode) VALUES (101, 'Seerah', 'seerah', 'shuffle')
		RETURNING id::text`).Scan(&ch); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO users (id, username, pw_hash, is_admin, display_name)
		VALUES (gen_random_uuid(), 'amina', 'x', true, 'Amina') RETURNING id::text`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	// the programmes are the admin's own recordings (story 50.23): check 3 counts them
	var lib, vid string
	if err := db.QueryRowContext(ctx, `INSERT INTO libraries (name, roots) VALUES ('Seerah', '{}') RETURNING id::text`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title,
		duration_sec, publishable_reason) VALUES ($1::uuid, encode(sha256('seerah'::bytea), 'hex'), '/m/seerah.mp4', 'seerah.mp4', 1, now(), 'Seerah', 2880, 'own')
		RETURNING id::text`, lib).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 24; i++ {
		start := now.Add(time.Duration(i) * time.Hour)
		end := start.Add(48 * time.Minute) // 80 % of each hour
		if _, err := db.ExecContext(ctx, `INSERT INTO channel_programs (channel_id, seq, start_at, end_at, source_duration, video_id)
			VALUES ($1::uuid, $2, $3, $4, 2880000, $5::uuid)`, ch, i+1, start, end, vid); err != nil {
			t.Fatal(err)
		}
	}
	return db, ch, admin
}

// T01 against the database: a refused publish starts nothing (no confirmation, no cloud
// channel, no token); the acceptance's invite publish goes live with its token, the
// confirmation is stored on the home and only its date and version go to the cloud.
func TestPublishOnTheHome(t *testing.T) {
	db, ch, admin := homeWithChannel(t)
	ctx := context.Background()
	cloud := &fakeCloud{plan: broadcasterPlan}
	svc := &Service{Store: &Store{DB: db}, Cloud: cloud}
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "seerah", Access: "invite", Mode: "on_demand"}); err != nil {
		t.Fatal(err)
	}

	// no uplink measured yet: refused, nothing started
	_, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"})
	var p *Problem
	if !errors.As(err, &p) || p.Refusal.Check != CheckUplink || p.Refusal.Code != CodeNoMeasurement {
		t.Fatalf("unmeasured: %v", err)
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_confirmations`).Scan(&n)
	if n != 0 || len(cloud.published) != 0 || cloud.tokens != 0 {
		t.Fatalf("a refusal started something: %d confirmations, %d publishes, %d tokens", n, len(cloud.published), cloud.tokens)
	}
	// without the tick: rights is named
	if err := svc.Store.AddUplink(ctx, 100_000, "publish"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, ch, false, Admin{UserID: admin, Name: "Amina"}); !errors.As(err, &p) || p.Refusal.Check != CheckRights {
		t.Fatalf("no tick: %v", err)
	}

	got, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateLive || got.PublishedBy != "Amina" || got.EdgeChannel == "" {
		t.Fatalf("published: %+v", got)
	}
	if len(cloud.published) != 1 {
		t.Fatalf("cloud publishes: %+v", cloud.published)
	}
	req := cloud.published[0]
	if req.Slug != "seerah" || req.Access != "invite" || !req.OnDemand || req.Rungs != 3 || req.TopKbps != 6192 ||
		req.AgreementVersion != 2 || req.RightsConfirmedAt.IsZero() {
		t.Fatalf("what the cloud got: %+v", req)
	}
	var by string
	var version int
	if err := db.QueryRowContext(ctx, `SELECT confirmed_by, agreement_version FROM broadcast_confirmations WHERE channel_id::text = $1`, ch).
		Scan(&by, &version); err != nil || by != "Amina" || version != 2 {
		t.Fatalf("confirmation %q %d %v", by, version, err)
	}
	var token string
	if err := db.QueryRowContext(ctx, `SELECT token FROM broadcast_ingest_tokens WHERE channel_id::text = $1`, ch).Scan(&token); err != nil || token != "mkbi1.tok" {
		t.Fatalf("token %q %v", token, err)
	}
	// a published channel's setup waits for an unpublish
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "other"}); !errors.As(err, &p) || p.Refusal.Code != "unpublishFirst" {
		t.Fatalf("edit while live: %v", err)
	}
}

// T08 at the API: with the cloud away the current token is kept until it expires (24 h),
// then the channel waits at its slate with broadcast.tokenExpired; the cloud back, it is
// live again. An unpublish the cloud could not take is sent again later.
func TestTokensAndPendingUnpublish(t *testing.T) {
	db, ch, admin := homeWithChannel(t)
	ctx := context.Background()
	cloud := &fakeCloud{plan: broadcasterPlan}
	now := time.Now()
	svc := &Service{Store: &Store{DB: db}, Cloud: cloud, Now: func() time.Time { return now }, QuietHour: 99}
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "seerah"}); err != nil {
		t.Fatal(err)
	}
	_ = svc.Store.AddUplink(ctx, 100_000, "publish")
	if _, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); err != nil {
		t.Fatal(err)
	}
	cloud.setDown(true)
	now = now.Add(3 * time.Hour) // 3 h away: a refresh fails, the token stands
	svc.Tick(ctx)
	if c, _ := svc.Store.Get(ctx, ch); c.State != StateLive || c.Detail != "" {
		t.Fatalf("after 3 h away: %s %s", c.State, c.Detail)
	}
	now = now.Add(22 * time.Hour) // 25 h: the token expired
	svc.Tick(ctx)
	if c, _ := svc.Store.Get(ctx, ch); c.State != StateOffline || c.Detail != "tokenExpired" {
		t.Fatalf("after 25 h away: %s %s", c.State, c.Detail)
	}
	cloud.setDown(false)
	svc.Tick(ctx)
	if c, _ := svc.Store.Get(ctx, ch); c.State != StateLive || c.Detail != "" {
		t.Fatalf("the cloud back: %s %s", c.State, c.Detail)
	}

	cloud.setDown(true)
	c, err := svc.Unpublish(ctx, ch)
	if err != nil || c.State != StateDraft {
		t.Fatalf("unpublish while away: %+v %v", c, err)
	}
	var tokens int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_ingest_tokens`).Scan(&tokens)
	if tokens != 0 {
		t.Fatal("the token outlived the unpublish")
	}
	if pend, _ := svc.Store.PendingUnpublish(ctx); len(pend) != 1 {
		t.Fatalf("pending: %v", pend)
	}
	cloud.setDown(false)
	svc.Tick(ctx)
	if pend, _ := svc.Store.PendingUnpublish(ctx); len(pend) != 0 || len(cloud.unpublished) != 1 {
		t.Fatalf("pending after the cloud came back: %v %v", pend, cloud.unpublished)
	}
}

// §3: the speed test PUTs to the edge with the channel's token, stores the rate, and a
// rate below the published channels' need drops the newest channel's top rung.
func TestSpeedTestStoresAndDrops(t *testing.T) {
	db, ch, admin := homeWithChannel(t)
	ctx := context.Background()
	var rate = 100_000
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/ingest/speedtest" || r.Header.Get("Authorization") != "Bearer mkbi1.tok" ||
			r.Header.Get("X-Maktaba-Time") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"bytes": n, "ms": 50, "kbps": rate})
	}))
	defer edge.Close()
	cloud := &fakeCloud{plan: broadcasterPlan, ingestURL: edge.URL}
	svc := &Service{Store: &Store{DB: db}, Cloud: cloud, SpeedTestFor: 50 * time.Millisecond}
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "seerah"}); err != nil {
		t.Fatal(err)
	}
	up, err := svc.SpeedTest(ctx, "publish")
	if err != nil || up.Kbps != 100_000 {
		t.Fatalf("first test: %+v %v", up, err)
	}
	if _, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); err != nil {
		t.Fatal(err)
	}
	rate = 12_000 // the ISP changed: 6 Mbps of budget for an 11.9 Mbps ladder
	if _, err := svc.SpeedTest(ctx, "daily"); err != nil {
		t.Fatal(err)
	}
	c, _ := svc.Store.Get(ctx, ch)
	if c.State != StateLive || c.Dropped != 1 || c.Detail != "rungDropped" {
		t.Fatalf("after the drop: %s dropped %d %q", c.State, c.Dropped, c.Detail)
	}
}
