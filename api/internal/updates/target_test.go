package updates

import (
	"encoding/json"
	"testing"
	"time"
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
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1}, {"v1.5.0", "1.5.0", 0}, {"1.5.0-rc.1", "1.5.0", -1},
		{"1.5.0-rc.10", "1.5.0-rc.9", 1}, {"1.5.0-beta", "1.5.0-rc", -1}, {"", "0.0.1", -1},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPolicyValidate(t *testing.T) {
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
	// phase A: the owner default is recorded, but this build does not install by itself
	if d.EffectiveAutoInstall() {
		t.Fatal("phase A build installs automatically")
	}
}

func rawMap(m map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		out[k] = json.RawMessage(v)
	}
	return out
}
