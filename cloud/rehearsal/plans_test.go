//go:build cloudrehearsal

package rehearsal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/plans"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// fetched is one relayed request: its status, the error code a refusal carries, the cap the
// home was asked to fit, how many bytes came and whether each was the home's, and how long.
type fetched struct {
	code    int
	errCode string
	cap     string
	bytes   int64
	intact  bool
	took    time.Duration
}

// fetch gets path from the home slug through the edge.
func (c *cluster) fetch(t *testing.T, slug, path string) fetched {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.edge+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = slug + ".relay.test"
	req.Header.Set("Accept", "application/json")
	start := time.Now()
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", slug, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	f := fetched{code: resp.StatusCode, cap: resp.Header.Get("X-Sim-Cap"), intact: true}
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		f.errCode = body.Error
		f.took = time.Since(start)
		return f
	}
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		for i := range n {
			if buf[i] != simhome.Pattern(int(f.bytes)+i) {
				f.intact = false
			}
		}
		f.bytes += int64(n)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("%s %s: after %d bytes: %v", slug, path, f.bytes, rerr)
		}
	}
	f.took = time.Since(start)
	return f
}

// C17 relay-plans (story 50.19 T13): homes on each plan behind the relay with the plans
// enforced, and the payment provider a fake. Free is refused (402 relay_needs_plan) with its
// tunnel kept; Basic's home is asked to fit 3 Mbit/s, its segments paced to it, and a third
// stream is refused (429 stream_limit) while the two playing go on; Pro past its allowance falls
// back to Basic's capped quality; Pro with pay-as-you-go on plays in full, and its gigabyte is
// sent to the provider once, through an outage. No stream stops in any: every segment comes
// whole.
//
// The homes are simulated (simhome): each says the cap it was asked to fit. The home server's
// own fit (renditions within the cap, a transcode when none is) is api/streaming's tests.
func relayPlans(t *testing.T, c *cluster) Result {
	ctx := context.Background()
	plans.Apply() // the reporter below prices pay-as-you-go as the relay under test does
	month := billing.MonthStart(time.Now())

	// homes 0 free, 1 basic, 2 pro past its allowance; 3 pro with pay-as-you-go, another account
	ids := map[string]string{}
	for i, plan := range []string{billing.PlanFree, billing.PlanBasic, billing.PlanPro} {
		got, err := seed.Homes(ctx, c.db, i, 1, plan)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range got {
			ids[k] = v
		}
	}
	owner, got, err := seed.HomesOf(ctx, c.db, "payg@example.test", 3, 1, billing.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range got {
		ids[k] = v
	}
	slug := func(i int) string { _, _, s := simhome.Creds(i); return s }
	free, basic, over, payg := slug(0), slug(1), slug(2), slug(3)
	for _, s := range []string{over, payg} { // this month's allowance used up
		if _, err := c.db.ExecContext(ctx, `INSERT INTO relay_usage_monthly (server_id, month, bytes_full) VALUES ($1, $2, $3)`,
			ids[s], month, plans.AllowanceBytes); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO subscriptions (user_id, plan, status, provider_subscription_id) VALUES ($1, 'pro', 'active', 'sub_rehearsal')`,
		`INSERT INTO payg_settings (user_id, enabled, cap_cents) VALUES ($1, true, 1000)`,
	} {
		if _, err := c.db.ExecContext(ctx, q, owner); err != nil {
			t.Fatal(err)
		}
	}
	c.startHomes(ids, 0, 4, 4)

	var requests, failed int64
	var notes []string
	check := func(what string, f fetched, ok bool) {
		t.Helper()
		requests++
		if !ok {
			failed++
			t.Errorf("%s: %+v", what, f)
		}
	}
	whole := func(f fetched, n int64) bool { return f.code == http.StatusOK && f.bytes == n && f.intact }

	// Free: refused, the tunnel kept
	f := c.fetch(t, free, "/api/health")
	check("free refused", f, f.code == http.StatusPaymentRequired && f.errCode == "relay_needs_plan")
	if n := c.tunnels(""); n != 4 {
		t.Errorf("a refusal took a tunnel: %d of 4", n)
	}

	// Basic: asked to fit 3 Mbit/s, paced to it (+20 %: 450 kB/s after a 900 kB burst)
	const seg = 2_250_000
	f = c.fetch(t, basic, "/stream/b1/720p/index.m3u8")
	check("basic's playlist carries the cap", f, f.code == http.StatusOK && f.cap == "3000")
	f = c.fetch(t, basic, fmt.Sprintf("/stream/b1/720p/seg-1.ts?bytes=%d", seg))
	check("basic's segment whole and paced", f, whole(f, seg) && f.cap == "3000" && f.took >= 2*time.Second)
	notes = append(notes, fmt.Sprintf("basic: %d bytes at 3 Mbit/s in %s", f.bytes, f.took.Round(time.Millisecond)))
	f = c.fetch(t, basic, "/stream/b2/720p/seg-1.ts?bytes=100000")
	check("basic's second stream", f, whole(f, 100_000))
	f = c.fetch(t, basic, "/stream/b3/720p/seg-1.ts?bytes=100000")
	check("basic's third stream refused", f, f.code == http.StatusTooManyRequests && f.errCode == "stream_limit")
	f = c.fetch(t, basic, "/stream/b1/720p/seg-2.ts?bytes=100000")
	check("basic's first stream plays on", f, whole(f, 100_000))
	f = c.fetch(t, basic, "/stream/b3/720p/index.m3u8")
	check("a playlist is never limited", f, f.code == http.StatusOK)

	// Pro past its allowance: Basic's capped quality, never stopped
	f = c.fetch(t, over, "/stream/o1/720p/index.m3u8")
	check("pro past its allowance is capped", f, f.code == http.StatusOK && f.cap == "3000")
	f = c.fetch(t, over, fmt.Sprintf("/stream/o1/720p/seg-1.ts?bytes=%d", seg))
	check("pro past its allowance plays on, paced", f, whole(f, seg) && f.took >= 2*time.Second)
	notes = append(notes, fmt.Sprintf("pro past its allowance: %d bytes capped in %s", f.bytes, f.took.Round(time.Millisecond)))

	// Pro with pay-as-you-go: full quality for a gigabyte and a bit
	const chunk = 110_000_000
	start := time.Now()
	for i := range 10 {
		f = c.fetch(t, payg, fmt.Sprintf("/stream/p1/1080p/seg-%d.ts?bytes=%d", i, chunk))
		check("pay-as-you-go plays in full", f, whole(f, chunk) && f.cap == "")
	}
	notes = append(notes, fmt.Sprintf("pay-as-you-go: %d MB in full in %s", 10*chunk/1_000_000, time.Since(start).Round(time.Millisecond)))
	var paygBytes int64
	waitFor(t, 2*time.Minute, "the meters' pay-as-you-go flush", func() bool {
		_ = c.db.QueryRowContext(ctx, `SELECT bytes_payg FROM relay_usage_monthly WHERE server_id = $1 AND month = $2`,
			ids[payg], month).Scan(&paygBytes)
		return paygBytes >= billing.GB
	})
	var otherPayg int64
	if err := c.db.QueryRowContext(ctx, `SELECT COALESCE(sum(bytes_payg), 0) FROM relay_usage_monthly WHERE server_id <> $1`,
		ids[payg]).Scan(&otherPayg); err != nil || otherPayg != 0 {
		t.Errorf("pay-as-you-go counted on a home without it: %d bytes (%v)", otherPayg, err)
	}

	// the gigabyte goes to the provider once: queued through its outage, sent, never again
	fake := &billing.Fake{Down: true}
	rep := &billing.Reporter{DB: c.db, Provider: fake}
	if n, err := rep.Enqueue(ctx, month); err != nil || n != 1 {
		t.Fatalf("queued %d (%v), want 1", n, err)
	}
	if n, err := rep.Send(ctx); err == nil || n != 0 {
		t.Errorf("sent %d through the provider's outage (%v)", n, err)
	}
	fake.Down = false
	if n, err := rep.Send(ctx); err != nil || n != 1 {
		t.Errorf("sent %d after the outage (%v), want 1", n, err)
	}
	if n, err := rep.Enqueue(ctx, month); err != nil || n != 0 {
		t.Errorf("queued again: %d (%v)", n, err)
	}
	if n, err := rep.Send(ctx); err != nil || n != 0 {
		t.Errorf("sent again: %d (%v)", n, err)
	}
	reports := fake.Reports()
	if len(reports) != 1 {
		t.Errorf("the provider has %d reports, want 1: %v", len(reports), reports)
	}
	for id, r := range reports {
		if r.GB != paygBytes/billing.GB || r.SubscriptionID != "sub_rehearsal" {
			t.Errorf("report %s: %+v, want %d GB on sub_rehearsal", id, r, paygBytes/billing.GB)
		}
		notes = append(notes, fmt.Sprintf("reported once: %s, %d GB", id, r.GB))
	}
	if n := c.tunnels(""); n != 4 {
		t.Errorf("tunnels at the end: %d of 4", n)
	}
	return Result{Requests: requests, FailedRequests: failed, Notes: notes}
}
