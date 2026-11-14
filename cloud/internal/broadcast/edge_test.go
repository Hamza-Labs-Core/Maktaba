package broadcast

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

// A window's key, end and counters must agree with its start; the cloud bills its full and
// lowest minutes under the same <publisher>:<window_start> key the edge sent.
func TestUsageWindowBilling(t *testing.T) {
	w := vectorWindow()
	b, err := w.Billing()
	if err != nil {
		t.Fatal(err)
	}
	if b.Publisher != w.Publisher || !b.Window.Equal(w.WindowStart) || b.MinutesFull != 12 || b.MinutesLowest != 3 ||
		billing.WindowKey(b.Publisher, b.Window) != w.Key {
		t.Fatalf("billed %+v", b)
	}
	// the key is RFC 3339 in whole seconds, as the edge writes it (never toISOString's .000Z)
	if w.Key != vectorPublisher+":2026-11-12T23:55:00Z" {
		t.Fatalf("key %q", w.Key)
	}
	for name, bad := range map[string]func(*UsageWindow){
		"a key with milliseconds": func(w *UsageWindow) { w.Key = vectorPublisher + ":2026-11-12T23:55:00.000Z" },
		"another publisher's key": func(w *UsageWindow) { w.Key = "x:" + strings.SplitN(w.Key, ":", 2)[1] },
		"a 10-minute window":      func(w *UsageWindow) { w.WindowEnd = w.WindowEnd.Add(5 * time.Minute) },
		"negative bytes":          func(w *UsageWindow) { w.BytesIn = -1 },
		"negative stream minutes": func(w *UsageWindow) { w.MinutesStream = -1 },
	} {
		c := vectorWindow()
		bad(&c)
		if _, err := c.Billing(); !errors.Is(err, ErrWindowShape) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The plan the edge holds follows the publisher's state; a later state has a higher version.
func TestPlanFromState(t *testing.T) {
	st := vectorState()
	p := PlanFromState(st)
	if p.Pub != vectorPublisher || p.Mode != "lowest" || p.MaxRungs != 4 || p.MaxTopKbps != 8000 || p.MaxViewers != 500 ||
		p.Version != vectorAt.UnixMilli() || p.Delivery != "" {
		t.Fatalf("plan %+v", p)
	}
	st.At = st.At.Add(time.Millisecond)
	if PlanFromState(st).Version <= p.Version {
		t.Fatal("a later state did not get a higher version")
	}
	// no plan: off, with no limits sent
	raw, _ := json.Marshal(PlanFromState(billing.PublisherState{Publisher: vectorPublisher, Mode: "off", At: vectorAt}))
	if string(raw) != `{"pub":"`+vectorPublisher+`","version":1794528000000,"mode":"off"}` {
		t.Fatalf("an off plan: %s", raw)
	}
}

// A signed plan push carries the Plan's fields, its version an integer, as a plan control post.
func TestSignPlan(t *testing.T) {
	k := testKey(t, "cloud-1")
	tok, err := SignPlan(k, PlanFromState(vectorState()), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _ := strings.Cut(strings.TrimPrefix(tok, ControlPrefix), ".")
	raw, err := b64.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":1794528000000`) || !strings.Contains(string(raw), `"typ":"plan"`) ||
		!strings.Contains(string(raw), `"max_viewers":500`) {
		t.Fatalf("payload %s", raw)
	}
	var c struct{ Exp, Iat int64 }
	_ = json.Unmarshal(raw, &c)
	if c.Exp-c.Iat != int64(PlanTTL/time.Second) {
		t.Fatalf("ttl %d", c.Exp-c.Iat)
	}
}
