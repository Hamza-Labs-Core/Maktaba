package components

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// gatedCatalog has a bundle that holds a gated component: none of the real bundles does
// today (cloud.relay is in none), the rule is for when one does.
func gatedCatalog(t *testing.T) *components.Catalog {
	t.Helper()
	def := components.Default()
	q := func(l []string) string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = fmt.Sprintf("%q", s)
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	comp := func(id, extra string) string {
		return fmt.Sprintf(`
[[component]]
id = %q
kind = "process"
group = "system"
title_key = "components.%s.title"
desc_key = "components.%s.desc"
processes = ["fake/%s"]
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
bundles = ["full"]
since = "0.4.0"
%s
`, id, id, id, id, extra)
	}
	src := fmt.Sprintf("schema = 1\nphases = %s\nreasons = %s\nfeatures = %s\n%s%s%s", q(def.Phases), q(def.Reasons),
		q(def.Features), comp("plain", ""), comp("paid", `licence = "cloud_relay"`), comp("paid2", `licence = "federation"`))
	c, err := components.Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Story 51.19 T05: a bundle that would turn on a component the licence lacks is refused
// whole, naming every such component; nothing of it is applied. The preview lists the
// refusal and the rest of the effect.
func TestBundleWithAGatedComponent(t *testing.T) {
	cat := gatedCatalog(t)
	rows := map[string]store.Desired{
		"plain": {ID: "plain", Want: components.WantInstalled, Generation: 1},
		"paid":  {ID: "paid", Want: components.WantInstalled, Generation: 1},
		"paid2": {ID: "paid2", Want: components.WantInstalled, Generation: 1},
	}
	none := func(string) bool { return false }
	_, err := makePlanTier(cat, none, "free", time.Now(), rows, 7, Request{Bundle: "full"}, who, false)
	r := refusalOf(t, err, CodeLicence, http.StatusForbidden)
	if !slices.Equal(r.Components, []string{"paid", "paid2"}) || r.Tier != "free" || r.ID != "paid" || r.Licence != "cloud_relay" {
		t.Fatalf("refusal %+v", r)
	}
	p, err := makePlanTier(cat, none, "free", time.Now(), rows, 7, Request{Bundle: "full"}, who, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.preview.Refused) != 1 || !slices.Equal(p.preview.Refused[0].Components, []string{"paid", "paid2"}) {
		t.Fatalf("preview refused %+v", p.preview.Refused)
	}
	if e, ok := effectOf(p, "plain"); !ok || e.To != components.PhaseRunning {
		t.Fatalf("preview effect %+v", p.preview.Effects)
	}
	// with the licence the bundle applies all three
	p, err = makePlanTier(cat, nil, "home", time.Now(), rows, 7, Request{Bundle: "full"}, who, false)
	if err != nil || len(p.writes) != 3 {
		t.Fatalf("licensed bundle: %v, %d writes", err, len(p.writes))
	}
}

// What the licence allows, as published on the rows: granted until expires_at +
// SkewGrace; inside the grace still granted; past it, revoked, or free: not granted, no
// until. cloud.link is never in it.
func TestLicences(t *testing.T) {
	cat := components.Default()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	lic := subscriptions.NewStore()
	lic.SetClock(func() time.Time { return now })
	got := Licences(cat, lic)
	if len(got) != 1 || got["cloud.relay"].Licensed || got["cloud.relay"].Until != nil {
		t.Fatalf("free: %+v", got)
	}
	exp := now.Add(31 * 24 * time.Hour)
	lic.Set(&subscriptions.Entitlements{Tier: subscriptions.TierHome, ExpiresAt: exp,
		Features: map[subscriptions.Feature]bool{subscriptions.FeatureCloudRelay: true}})
	got = Licences(cat, lic)
	l := got["cloud.relay"]
	if !l.Licensed || l.Until == nil || !l.Until.Equal(exp.Add(subscriptions.SkewGrace)) {
		t.Fatalf("home: %+v", l)
	}
	if _, ok := got["cloud.link"]; ok {
		t.Fatal("cloud.link is gated")
	}
	now = exp.Add(time.Hour) // inside the grace
	if l := Licences(cat, lic)["cloud.relay"]; !l.Licensed {
		t.Fatal("inside the grace: not granted")
	}
	now = exp.Add(subscriptions.SkewGrace) // the grace is over
	if l := Licences(cat, lic)["cloud.relay"]; l.Licensed || l.Until != nil {
		t.Fatalf("past the grace: %+v", l)
	}
}

// An enabled gated component reads Waiting, licence_missing, in the view and the effect.
func TestGatesWithTheLicence(t *testing.T) {
	rows := allOn()
	none := func(string) bool { return false }
	g := gatesLicensed(components.Default(), rows, none)
	if g["cloud.relay"].on || g["cloud.relay"].phase != components.PhaseWaiting || g["cloud.relay"].reason != CodeLicence {
		t.Fatalf("cloud.relay %+v", g["cloud.relay"])
	}
	if !g["cloud.link"].on {
		t.Fatal("cloud.link gated")
	}
	if g := gatesLicensed(components.Default(), rows, nil); !g["cloud.relay"].on {
		t.Fatal("every licence: cloud.relay off")
	}
}
