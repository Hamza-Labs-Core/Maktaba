package moderation

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

func complete() Notice {
	at := time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)
	return Notice{ClaimantName: "Studio A", ClaimantEmail: "rights@studio.test", ClaimantAddress: "1 Road, City",
		Work: "Film X", URLs: []string{"https://watch.maktaba.tv/home-a/news"}, At: &at, Explanation: "the whole film",
		GoodFaith: true, Accuracy: true, Signature: "A. Person"}
}

// T01: each required element missing is named in the reply, and a complete notice names none.
func TestNoticeMissingNamesEachElement(t *testing.T) {
	if m := complete().Missing(); len(m) != 0 {
		t.Fatalf("a complete notice misses %v", m)
	}
	cases := map[string]func(*Notice){
		MissingName:        func(n *Notice) { n.ClaimantName = " " },
		MissingEmail:       func(n *Notice) { n.ClaimantEmail = "not-an-email" },
		MissingAddress:     func(n *Notice) { n.ClaimantAddress = "" },
		MissingWork:        func(n *Notice) { n.Work = "" },
		MissingURL:         func(n *Notice) { n.URLs = []string{" "} },
		MissingExplanation: func(n *Notice) { n.Explanation = "" },
		MissingGoodFaith:   func(n *Notice) { n.GoodFaith = false },
		MissingAccuracy:    func(n *Notice) { n.Accuracy = false },
		MissingSignature:   func(n *Notice) { n.Signature = "" },
	}
	for want, breakIt := range cases {
		n := complete()
		breakIt(&n)
		if m := n.Missing(); !slices.Equal(m, []string{want}) {
			t.Errorf("without %s: missing %v", want, m)
		}
	}
	if m := (Notice{}).Missing(); len(m) != len(cases) {
		t.Errorf("an empty notice misses %d elements, want %d: %v", len(m), len(cases), m)
	}
}

// T02 (the URL half): a watch URL names a publisher and a channel; anything else is not Maktaba.
func TestParseWatchURL(t *testing.T) {
	good := map[string]ChannelRef{
		"https://watch.maktaba.tv/home-a/news":            {"home-a", "news"},
		"watch.maktaba.tv/Home-A/News/live?x=1#t":         {"home-a", "news"},
		"http://WATCH.maktaba.tv/home-a/quran-24/":        {"home-a", "quran-24"},
		"https://watch.maktaba.tv/home-a/news?t=20:00:00": {"home-a", "news"},
	}
	for u, want := range good {
		if got, ok := ParseWatchURL(u); !ok || got != want {
			t.Errorf("%s = %+v %v, want %+v", u, got, ok, want)
		}
	}
	for _, u := range []string{"", "https://maktaba.tv/home-a/news", "https://watch.maktaba.tv/home-a",
		"https://watch.maktaba.tv/i/abc", "https://watch.maktaba.tv/pair/x", "https://evil.test/watch.maktaba.tv/a/b",
		"ftp://watch.maktaba.tv/a/b", "https://watch.maktaba.tv/a_b/c"} {
		if _, ok := ParseWatchURL(u); ok {
			t.Errorf("%q reads as a channel", u)
		}
	}
	if _, ok := OnMaktaba([]string{"https://youtube.test/v/1", "https://watch.maktaba.tv/home-a/news"}); !ok {
		t.Error("a notice with one Maktaba URL among others is not on Maktaba")
	}
	if _, ok := OnMaktaba([]string{"https://youtube.test/v/1"}); ok {
		t.Error("a notice about another site reads as on Maktaba")
	}
}

// T02 (the guide half): the time resolves to the programme airing then, or "unknown".
func TestProgrammeAtTheGuide(t *testing.T) {
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	guide := []GuideEntry{
		{Start: day.Add(19 * time.Hour), End: day.Add(20 * time.Hour), Title: "News", ItemRef: "item-1"},
		{Start: day.Add(20 * time.Hour), End: day.Add(22 * time.Hour), Title: "Film X", ItemRef: "item-2"},
	}
	if g, ok := ProgrammeAt(guide, day.Add(20*time.Hour)); !ok || g.ItemRef != "item-2" {
		t.Errorf("20:00 = %+v %v, want item-2 (a programme starts at its start)", g, ok)
	}
	if g, ok := ProgrammeAt(guide, day.Add(19*time.Hour+59*time.Minute)); !ok || g.ItemRef != "item-1" {
		t.Errorf("19:59 = %+v %v", g, ok)
	}
	if _, ok := ProgrammeAt(guide, day.Add(23*time.Hour)); ok {
		t.Error("a time with no slot resolved")
	}
}

// T05: 1, 2 and 3 strikes in 12 months are a notice, a warning and the end; a withdrawn strike
// and an expired one don't count.
func TestStrikeLevels(t *testing.T) {
	p := DefaultPolicy
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	strike := func(ago time.Duration, withdrawn bool) Strike {
		at := now.Add(-ago)
		return Strike{At: at, ExpiresAt: p.Expiry(at), Withdrawn: withdrawn}
	}
	day := 24 * time.Hour
	if p.Level(0) != LevelNone || p.Level(1) != LevelNotice || p.Level(2) != LevelWarning || p.Level(3) != LevelTerminate || p.Level(4) != LevelTerminate {
		t.Fatal("levels")
	}
	list := []Strike{strike(10*day, false), strike(100*day, false)}
	if n := Active(list, now); p.Level(n) != LevelWarning {
		t.Fatalf("two strikes: %d", n)
	}
	list = append(list, strike(day, true)) // withdrawn
	list = append(list, strike(370*day, false))
	if n := Active(list, now); n != 2 {
		t.Fatalf("a withdrawn and an expired strike count: %d", n)
	}
	list = append(list, strike(0, false))
	if n := Active(list, now); p.Level(n) != LevelTerminate {
		t.Fatalf("the third active strike: %d", n)
	}
	// the daily view: a strike exactly 12 months old no longer counts
	old := strike(0, false)
	if Active([]Strike{old}, p.Expiry(old.At)) != 0 {
		t.Error("a strike 12 months old still counts")
	}
}

// T06: the restore window opens 10 business days after the counter-notice, never on a lawsuit.
func TestRestoreWindowBusinessDays(t *testing.T) {
	fri := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC) // a Friday
	if got := AddBusinessDays(fri, 10); !got.Equal(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("Friday + 10 business days = %s", got)
	}
	if got := AddBusinessDays(fri, 1); got.Weekday() != time.Monday {
		t.Errorf("Friday + 1 business day = %s", got.Weekday())
	}
	open := AddBusinessDays(fri, RestoreAfterDays)
	if RestoreOpen(fri, false, open.Add(-time.Second)) || !RestoreOpen(fri, false, open) {
		t.Error("the window's edge")
	}
	if RestoreOpen(fri, true, open.Add(30*24*time.Hour)) {
		t.Error("a lawsuit still offers restore")
	}
}

// The home's instructions are a control post of their own type, signed with the broadcast key.
func TestSignInstructions(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	k := bc.Key{Kid: "k1", Priv: priv}
	at := time.Now().UTC().Truncate(time.Second)
	text, err := SignInstructions(k, "srv-1", []Instruction{{ID: "a-1", Kind: "takedown", Scope: ScopeItem, Channel: "c-1", At: &at}}, at)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(text, bc.ControlPrefix)
	if !ok {
		t.Fatalf("not a control post: %s", text)
	}
	body, sig, _ := strings.Cut(rest, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	s, _ := base64.RawURLEncoding.DecodeString(sig)
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), []byte(bc.ControlDomain+"."+body), s) {
		t.Fatal("the signature does not verify")
	}
	var p struct {
		Typ          string        `json:"typ"`
		Home         string        `json:"home"`
		Instructions []Instruction `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Typ != InstructionsType || p.Home != "srv-1" || len(p.Instructions) != 1 ||
		p.Instructions[0].ID != "a-1" || !p.Instructions[0].At.Equal(at) {
		t.Fatalf("payload %+v %v", p, err)
	}
	empty, _ := SignInstructions(k, "srv-1", nil, at)
	if !strings.HasPrefix(empty, bc.ControlPrefix) {
		t.Fatal("an empty batch is not signed")
	}
}

// T09 (the mails): every mail has its English and its Arabic, filled in.
func TestMailsInEnglishAndArabic(t *testing.T) {
	for kind := range mails {
		body, ok := MailText(kind, map[string]any{"programme": "Film X", "count": 2, "limit": 3, "claimant": "",
			"notice_id": "n-1", "name": "P", "address": "A", "explanation": "E", "restore_after": "2026-11-27", "scope": "item"})
		if !ok || strings.Contains(body, "{") {
			t.Errorf("%s: %q", kind, body)
		}
		en, ar, found := strings.Cut(body, "———")
		if !found || strings.TrimSpace(en) == "" || !strings.ContainsAny(ar, "ابتثجحخدذرزسشصضطظعغفقكلمنهوي") {
			t.Errorf("%s has no Arabic half: %q", kind, body)
		}
	}
	if _, ok := MailText("nope", nil); ok {
		t.Error("an unknown mail")
	}
	if Urgent(CategoryCopyright) || !Urgent(CategoryIllegal) {
		t.Error("only illegal content is urgent")
	}
}
