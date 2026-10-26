package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// `maktaba-api license apply` checks a license as the Settings page does: a good one is
// applied and audited, anything else is refused with a reason and changes nothing.
func TestLicenseApplyFromTheCommandLine(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := &subscriptions.Verifier{PublicKey: pub}
	now := time.Now().UTC()
	lic, err := subscriptions.Sign(priv, subscriptions.LicenseInner{LicenseID: "owner-1", Tier: subscriptions.TierPro,
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(10, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(lic)
	var audited []string
	audit := func(_ context.Context, _, event string, d map[string]any) {
		audited = append(audited, event+" "+d["license_id"].(string)+" "+d["via"].(string))
	}

	refused := []struct{ name, body string }{
		{"not JSON", "hello"},
		{"tampered", strings.Replace(string(raw), `"owner-1"`, `"owner-2"`, 1)},
	}
	for _, c := range refused {
		store := subscriptions.NewStore()
		if err := applyLicense(context.Background(), store, v, audit, []byte(c.body), &bytes.Buffer{}); err == nil {
			t.Errorf("%s: applied", c.name)
		}
		if store.Current() != nil {
			t.Errorf("%s: the store changed", c.name)
		}
	}
	if len(audited) != 0 {
		t.Fatalf("a refused license was audited: %v", audited)
	}

	store := subscriptions.NewStore()
	var out bytes.Buffer
	if err := applyLicense(context.Background(), store, v, audit, raw, &out); err != nil {
		t.Fatal(err)
	}
	if cur := store.Current(); cur == nil || cur.LicenseID != "owner-1" || store.SeatLimit() != subscriptions.SeatsUnlimited {
		t.Fatalf("not applied: %+v", cur)
	}
	if !strings.Contains(out.String(), "pro plan, unlimited users, active") {
		t.Fatalf("output: %q", out.String())
	}
	if len(audited) != 1 || audited[0] != "license.applied owner-1 cli" {
		t.Fatalf("audit: %v", audited)
	}
}
