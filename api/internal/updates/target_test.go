package updates

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func rel(v string, mods ...func(*Release)) Release {
	pub := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r := Release{Version: v, Tag: "v" + v, Channel: ChannelStable, VerifyState: VerifyVerified,
		Manifest: &Manifest{V: 1, Version: v, Tag: "v" + v, Channel: ChannelStable, Published: pub}}
	if IsPrerelease(v) {
		r.Channel, r.Prerelease, r.Manifest.Channel = ChannelBeta, true, ChannelBeta
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func minFrom(v string) func(*Release) { return func(r *Release) { r.Manifest.MinUpgradeFrom = v } }

var contract = func(r *Release) { r.Manifest.BreaksPreviousHome = true }

// TC1 (story 35.3): the target is a pure function of catalog, policy, current version
// and watermark.
func TestChooseTarget(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pol := DefaultPolicy()
	beta := pol
	beta.Channel = ChannelBeta
	pinned := pol
	pinned.PinnedVersion = "1.5.9"
	skipped := pol
	skipped.SkippedVersions = []string{"1.6.0"}
	cases := []struct {
		name      string
		catalog   []Release
		pol       Policy
		current   string
		watermark string
		want      string // "" = no target
		held      bool
		stone     bool
		reason    string
	}{
		{"highest verified wins, numerically", []Release{rel("1.9.0"), rel("1.10.0"), rel("1.2.0")}, pol, "1.4.0", "", "1.10.0", false, false, ""},
		{"nothing newer", []Release{rel("1.4.0"), rel("1.3.0")}, pol, "1.4.0", "1.4.0", "", false, false, ReasonUpToDate},
		{"unverified and failed never chosen", []Release{
			rel("1.6.0", func(r *Release) { r.VerifyState = VerifyUnverified }),
			rel("1.7.0", func(r *Release) { r.VerifyState = VerifyFailed }),
			rel("1.5.0")}, pol, "1.4.0", "", "1.5.0", false, false, ""},
		{"withdrawn skipped", []Release{rel("1.6.0", func(r *Release) { r.WithdrawnAt = &now }), rel("1.5.0")}, pol, "1.4.0", "", "1.5.0", false, false, ""},
		{"blocked skipped", []Release{rel("1.6.0", func(r *Release) { r.BlockedReason = "too_many_failures" }), rel("1.5.0")}, pol, "1.4.0", "", "1.5.0", false, false, ""},
		{"no manifest (pre-35.1 release) never chosen", []Release{rel("1.6.0", func(r *Release) { r.Manifest = nil })}, pol, "1.4.0", "", "", false, false, ReasonUpToDate},
		{"stable skips prereleases", []Release{rel("1.6.0-rc.1"), rel("1.5.0")}, pol, "1.4.0", "", "1.5.0", false, false, ""},
		{"beta takes prereleases", []Release{rel("1.6.0-rc.1"), rel("1.5.0")}, beta, "1.4.0", "", "1.6.0-rc.1", false, false, ""},
		{"beta -> stable never lower than the watermark", []Release{rel("1.6.0-rc.1"), rel("1.5.0")}, pol, "1.6.0-rc.1", "1.6.0-rc.1", "", false, false, ReasonUpToDate},
		{"watermark above current excludes lower", []Release{rel("1.5.0"), rel("1.6.0")}, pol, "1.4.0", "1.5.0", "1.6.0", false, false, ""},
		{"skip", []Release{rel("1.6.0"), rel("1.5.0")}, skipped, "1.4.0", "", "1.5.0", false, false, ""},
		{"pin caps", []Release{rel("1.6.0"), rel("1.5.9"), rel("1.5.0")}, pinned, "1.4.0", "", "1.5.9", false, false, ""},
		{"stepping stone", []Release{rel("1.8.0", minFrom("1.6.0")), rel("1.6.0", minFrom("1.3.0")), rel("1.5.0")}, pol, "1.4.0", "", "1.6.0", false, true, ""},
		{"no upgrade path", []Release{rel("1.8.0", minFrom("1.6.0"))}, pol, "1.4.0", "", "", false, false, ReasonNoUpgradeFor},
		{"contract release held", []Release{rel("2.0.0", contract), rel("1.5.0")}, pol, "1.4.0", "", "2.0.0", true, false, ReasonHeld},
		{"approved contract release not held", []Release{rel("2.0.0", contract, func(r *Release) { r.ApprovedAt = &now })}, pol, "1.4.0", "", "2.0.0", false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ChooseTarget(c.catalog, c.pol, c.current, c.watermark)
			v := ""
			if got.Target != nil {
				v = got.Target.Version
			}
			if v != c.want || got.Held != c.held || got.SteppingStone != c.stone || got.Reason != c.reason {
				t.Fatalf("got target %q held=%v stone=%v reason=%q; want %q held=%v stone=%v reason=%q",
					v, got.Held, got.SteppingStone, got.Reason, c.want, c.held, c.stone, c.reason)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1}, {"v1.5.0", "1.5.0", 0}, {"1.5.0-rc.1", "1.5.0", -1},
		{"1.5.0-rc.10", "1.5.0-rc.9", 1}, {"1.5.0-beta", "1.5.0-rc", -1}, {"", "0.0.1", -1},
		// a checkout's build is after the release it was built from, as the verifier says
		{"0.2.0", "0.2.0-12-gabc1234", -1}, {"0.2.1", "0.2.0-12-gabc1234", 1},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// so the catalog never offers a checkout's own release as newer
	if got := maxVersion("0.2.0-12-gabc1234", "0.2.0"); got != "0.2.0-12-gabc1234" {
		t.Errorf("maxVersion = %q", got)
	}
	if IsPrerelease("0.2.0-12-gabc1234") || !IsPrerelease("1.0.0-rc.1") {
		t.Error("IsPrerelease: a build off a release tag is not a prerelease, an rc is")
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	p.PinnedVersion = "1.4.0"
	if err := p.Validate("1.5.0"); err == nil || policyErrCode(err) != "pin_below_installed" {
		t.Fatalf("pin below installed: %v", err)
	}
	p.PinnedVersion = "1.5.0"
	if err := p.Validate("1.5.0"); err != nil {
		t.Fatalf("pin at installed: %v", err)
	}
	p.PinnedVersion = "latest"
	if err := p.Validate(""); policyErrCode(err) != "bad_pin" {
		t.Fatalf("bad pin: %v", err)
	}
	p.PinnedVersion, p.Window = "", "3am"
	if err := p.Validate(""); policyErrCode(err) != "bad_window" {
		t.Fatalf("bad window: %v", err)
	}
	p.Window, p.Channel = "03:00-05:00", "nightly"
	if err := p.Validate(""); policyErrCode(err) != "bad_channel" {
		t.Fatalf("bad channel: %v", err)
	}
}

// AC1: MAKTABA_UPDATE_* is the base, the settings override it.
func TestPolicyEnvBaseSettingsOverride(t *testing.T) {
	env := map[string]string{"MAKTABA_UPDATE_CHANNEL": "beta", "MAKTABA_UPDATE_WINDOW": "01:00-02:00",
		"MAKTABA_UPDATE_MIN_AGE_HOURS": "6", "MAKTABA_UPDATE_AUTO_INSTALL": "false"}
	p := DefaultPolicy().ApplyEnv(func(k string) string { return env[k] })
	if p.Channel != ChannelBeta || p.Window != "01:00-02:00" || p.MinReleaseAgeHours != 6 || p.AutoInstall {
		t.Fatalf("env base not applied: %+v", p)
	}
	q := p.ApplyOverrides(rawMap(map[string]string{"window": `"03:00-05:00"`, "auto_install": "true",
		"deferred_until": `"2026-10-24T03:00:00Z"`, "skipped_versions": `["1.6.0"]`}))
	if q.Window != "03:00-05:00" || !q.AutoInstall || q.DeferredUntil == nil || !q.Skipped("v1.6.0") || q.Channel != ChannelBeta {
		t.Fatalf("settings did not override: %+v", q)
	}
	d := DefaultPolicy()
	if !d.AutoInstall || d.Channel != "stable" || d.Window != "03:00-05:00" || d.MinReleaseAgeHours != 24 ||
		d.WindowOverrunMinutes != 120 || d.KeepVersions != 3 || d.NodeFailurePolicy != "hold_platform" ||
		d.ProxyCacheMB != 2048 || d.CatchUp != "immediate" || !d.RehearseMigrations || d.RehearseMaxDBGB != 20 {
		t.Fatalf("defaults differ from the README: %+v", d)
	}
	// phase B: this build installs by itself (rules.updates.auto_install, default on)
	if !d.EffectiveAutoInstall() {
		t.Fatal("this build does not install automatically")
	}
	disableAutoInstall(t)
	if d.EffectiveAutoInstall() {
		t.Fatal("a build with automatic installs turned off installs automatically")
	}
}

func rawMap(m map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		out[k] = json.RawMessage(v)
	}
	return out
}

// One "never go back" rule (releaseverify.OlderBuild): rc.7 was tagged after v0.3.0, so a
// home on rc.7 does not take 0.3.0 (newer in version, the older build) but takes 0.3.1; a
// hotfix on an older minor signed after the next minor still moves up to it (22.5 EC1).
func TestChooseTargetPassesAnOlderBuild(t *testing.T) {
	t.Parallel()
	at := func(s string) func(*Release) {
		return func(r *Release) {
			p, _ := time.Parse(time.RFC3339, s)
			r.Manifest.Published = p
		}
	}
	beta := DefaultPolicy()
	beta.Channel = ChannelBeta
	cat := []Release{rel("0.3.0-rc.6", at("2026-10-24T05:16:46Z")), rel("0.3.0", at("2026-10-24T05:43:50Z")),
		rel("0.3.0-rc.7", at("2026-10-24T06:17:21Z"))}
	for _, pol := range []Policy{DefaultPolicy(), beta} {
		if c := ChooseTarget(cat, pol, "0.3.0-rc.7", ""); c.Target != nil || c.Reason != ReasonUpToDate {
			t.Fatalf("%s: a home on rc.7 took the older build: %+v", pol.Channel, c.Target)
		}
	}
	// a machine on rc.6 (signed before 0.3.0) still takes 0.3.0
	if c := ChooseTarget(cat, DefaultPolicy(), "0.3.0-rc.6", ""); c.Target == nil || c.Target.Version != "0.3.0" {
		t.Fatalf("rc.6: %+v", c.Target)
	}
	cat = append(cat, rel("0.3.1", at("2026-10-24T17:56:46Z")))
	if c := ChooseTarget(cat, DefaultPolicy(), "0.3.0-rc.7", ""); c.Target == nil || c.Target.Version != "0.3.1" {
		t.Fatalf("0.3.1: %+v", c.Target)
	}
	// the floor is the watermark when it is higher: an rc.7 watermark passes 0.3.0 by
	if c := ChooseTarget(cat[:3], DefaultPolicy(), "0.3.0-rc.6", "0.3.0-rc.7"); c.Target != nil {
		t.Fatalf("watermark rc.7: %+v", c.Target)
	}
	// a hotfix on 0.2 signed after 0.3.0 moves up to it
	hot := []Release{rel("0.2.5", at("2026-10-25T00:00:00Z")), rel("0.3.0", at("2026-10-24T05:43:50Z"))}
	if c := ChooseTarget(hot, DefaultPolicy(), "0.2.5", ""); c.Target == nil || c.Target.Version != "0.3.0" {
		t.Fatalf("hotfix line: %+v", c.Target)
	}
	// a running release the catalog does not know (or has not verified): no time rule
	if c := ChooseTarget(cat[1:2], DefaultPolicy(), "0.3.0-rc.7", ""); c.Target == nil || c.Target.Version != "0.3.0" {
		t.Fatalf("unknown running release: %+v", c.Target)
	}
}

// R4 (audit): the rule compares the verified signing times (the transparency log's, as the
// machines do), not release.json's "published": a release.json that says otherwise does
// not decide; the verification keeps that time in the catalog.
func TestChooseTargetComparesTheSigningTime(t *testing.T) {
	t.Parallel()
	ts := func(s string) time.Time { p, _ := time.Parse(time.RFC3339, s); return p }
	signed := func(published, signedAt string) func(*Release) {
		return func(r *Release) {
			r.Manifest.Published = ts(published)
			r.Manifest.SignedAt = ts(signedAt)
		}
	}
	// release.json's times say rc.7 came first; the signing times say 0.3.0 did
	cat := []Release{rel("0.3.0", signed("2026-10-24T07:00:00Z", "2026-10-24T05:44:07Z")),
		rel("0.3.0-rc.7", signed("2026-10-24T05:00:00Z", "2026-10-24T06:17:37Z"))}
	if c := ChooseTarget(cat, DefaultPolicy(), "0.3.0-rc.7", ""); c.Target != nil {
		t.Fatalf("an older build by its signing time was taken: %+v", c.Target)
	}

	store := &Store{DB: sqliteDB(t)}
	ctx := context.Background()
	seed(t, store, rel("0.3.1"))
	m := Manifest{V: 1, Version: "0.3.1", Tag: "v0.3.1", Published: ts("2026-10-24T17:56:46Z"),
		SignedAt: ts("2000-01-01T00:00:00Z")} // a release.json naming a time is overridden
	if err := store.SetVerified(ctx, "0.3.1", Verified{Manifest: m, ChecksumsSHA256: "c",
		SignedAt: ts("2026-10-24T17:57:06Z")}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "0.3.1")
	if err != nil || got.Manifest == nil || !got.Manifest.SignedAt.Equal(ts("2026-10-24T17:57:06Z")) ||
		!got.signedTime().Equal(ts("2026-10-24T17:57:06Z")) {
		t.Fatalf("the verified signing time: %+v %v", got.Manifest, err)
	}
}

func blocked(reason string) func(*Release) { return func(r *Release) { r.BlockedReason = reason } }

func migrations(set string) func(*Release) {
	return func(r *Release) {
		r.Manifest.Migrations = &releaseverify.Migrations{Archive: "maktaba-migrations-" + r.Version + ".tar.gz",
			SHA256: strings.Repeat("0", 64), SetSHA256: set}
	}
}

// The incident of 2026-11-24: v0.3.153 runs, v0.3.158 rolled back the day before (0235's
// lock timeout), and the home chose v0.3.157, which lacked the same fix, rolled back
// again (a 3-hour outage), and had v0.3.156 and v0.3.155 next. A release below one that
// failed here is never chosen by itself, nor one above it with its migrations until one
// changes them; the newest release that is neither is.
func TestChooseTargetHoldsBackBelowAndBesideAFailedRelease(t *testing.T) {
	t.Parallel()
	setA, setB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	p := DefaultPolicy()
	for _, tc := range []struct {
		name       string
		catalog    []Release
		current    string
		want       string
		reason     string
		heldBackBy string
	}{
		{name: "the incident's catalog: nothing older than the rolled-back release", current: "0.3.153",
			catalog: []Release{rel("0.3.153"), rel("0.3.154"), rel("0.3.155"), rel("0.3.156"), rel("0.3.157"),
				rel("0.3.158", blocked(BlockedRolledBack))},
			reason: ReasonHeldBack, heldBackBy: "0.3.158"},
		{name: "two rolled back: still nothing older", current: "0.3.153",
			catalog: []Release{rel("0.3.154"), rel("0.3.155"), rel("0.3.156"),
				rel("0.3.157", blocked(BlockedRolledBack)), rel("0.3.158", blocked(BlockedRolledBack))},
			reason: ReasonHeldBack, heldBackBy: "0.3.158"},
		{name: "a newer release is chosen, past the ones in between", current: "0.3.153",
			catalog: []Release{rel("0.3.155"), rel("0.3.157"), rel("0.3.158", blocked(BlockedRolledBack)),
				rel("0.3.159"), rel("0.3.160"), rel("0.3.161")},
			want: "0.3.161", heldBackBy: "0.3.158"},
		{name: "its migrations failed on a copy: the same", current: "0.3.153",
			catalog: []Release{rel("0.3.156"), rel("0.3.158", blocked(BlockedRehearsal))},
			reason:  ReasonHeldBack, heldBackBy: "0.3.158"},
		{name: "newer releases with the failed one's migrations are held back", current: "0.3.153",
			catalog: []Release{rel("0.3.158", blocked(BlockedRolledBack), migrations(setA)),
				rel("0.3.159", migrations(setA)), rel("0.3.160", migrations(setA))},
			reason: ReasonHeldBack, heldBackBy: "0.3.158"},
		{name: "until one changes them", current: "0.3.153",
			catalog: []Release{rel("0.3.158", blocked(BlockedRolledBack), migrations(setA)),
				rel("0.3.159", migrations(setA)), rel("0.3.160", migrations(setB)), rel("0.3.161", migrations(setA))},
			want: "0.3.160", heldBackBy: "0.3.158"},
		{name: "a release whose set is not known is not held back by it", current: "0.3.153",
			catalog: []Release{rel("0.3.158", blocked(BlockedRolledBack), migrations(setA)), rel("0.3.159")},
			want:    "0.3.159", heldBackBy: "0.3.158"},
		{name: "a failed release without a set holds back no newer one", current: "0.3.153",
			catalog: []Release{rel("0.3.158", blocked(BlockedRolledBack)), rel("0.3.159", migrations(setA))},
			want:    "0.3.159", heldBackBy: "0.3.158"},
		{name: "a release that failed at or below the running one holds nothing back", current: "0.3.158",
			catalog: []Release{rel("0.3.157", blocked(BlockedRolledBack), migrations(setA)),
				rel("0.3.158", migrations(setA)), rel("0.3.159", migrations(setA))},
			want: "0.3.159"},
		{name: "blocked after failed downloads (attempts): the release below is still chosen", current: "0.3.153",
			catalog: []Release{rel("0.3.157"), rel("0.3.158", blocked("attempts"))},
			want:    "0.3.157"},
		{name: "a withdrawn release holds nothing back", current: "0.3.153",
			catalog: []Release{rel("0.3.157"), rel("0.3.158", blocked(WithdrawnPrerelease))},
			want:    "0.3.157"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := ChooseTarget(tc.catalog, p, tc.current, "")
			got := ""
			if c.Target != nil {
				got = c.Target.Version
			}
			if got != tc.want || (tc.reason != "" && c.Reason != tc.reason) || c.HeldBackBy != tc.heldBackBy {
				t.Fatalf("target %q reason %q held back by %q; want %q %q %q", got, c.Reason, c.HeldBackBy,
					tc.want, tc.reason, tc.heldBackBy)
			}
		})
	}
}

// An admin's Update now naming a release below one that failed installs it: RunNow
// chooses from that release alone.
func TestChooseTargetHeldBackReleaseByName(t *testing.T) {
	t.Parallel()
	only := []Release{rel("0.3.157")}
	if c := ChooseTarget(only, DefaultPolicy(), "0.3.153", ""); c.Target == nil || c.Target.Version != "0.3.157" {
		t.Fatalf("Update now naming 0.3.157: %+v", c)
	}
}

// The plan says why nothing installs: held back, not up to date.
func TestMakePlanSaysHeldBack(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	pl := MakePlan(now, time.UTC, DefaultPolicy(), []Release{rel("0.3.157"),
		rel("0.3.158", blocked(BlockedRolledBack))}, "0.3.153", "")
	if pl.Target != nil || pl.Due || pl.Waiting != ReasonHeldBack || pl.HeldBackBy != "0.3.158" {
		t.Fatalf("plan = %+v", pl)
	}
}
