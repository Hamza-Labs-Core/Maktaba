package devices

import (
	"encoding/json"
	"strings"
	"testing"
)

// R4.3: GET /api/devices must never leak the raw APNs/FCM push token.
// redactDevice is the response-boundary transform List applies to every
// row before serialisation; it mirrors settings.redactSecrets — replace
// the value with "<redacted>" and add a sibling "<key>_present": true.
func TestRedactDevice_HidesPushToken(t *testing.T) {
	d := Device{
		ID:        "dev-1",
		UserID:    "user-1",
		Platform:  "ios",
		PushToken: "secrettoken",
		BundleID:  "dev.maktaba.app",
	}

	out := redactDevice(d)

	if got := out["push_token"]; got != "<redacted>" {
		t.Fatalf("push_token = %v, want <redacted>", got)
	}
	if got := out["push_token_present"]; got != true {
		t.Fatalf("push_token_present = %v, want true", got)
	}

	// The raw secret must not survive serialisation anywhere.
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "secrettoken") {
		t.Fatalf("raw push token leaked in response body: %s", body)
	}

	// Non-secret fields are preserved.
	if out["id"] != "dev-1" || out["platform"] != "ios" {
		t.Fatalf("non-secret fields mangled: %#v", out)
	}
}

func TestRedactDevice_AbsentTokenStillMarkedNotPresent(t *testing.T) {
	d := Device{ID: "dev-2", Platform: "web", PushToken: ""}
	out := redactDevice(d)
	if out["push_token"] != "<redacted>" {
		t.Fatalf("push_token = %v, want <redacted>", out["push_token"])
	}
	if out["push_token_present"] != false {
		t.Fatalf("push_token_present = %v, want false", out["push_token_present"])
	}
}

// Story 12.10 AC: `400 invalid-token` when the token format does not
// match the declared platform. APNs tokens are 64 hex chars; FCM tokens
// are long opaque strings (allow ≥ 32, URL-safe set); web push tokens
// (endpoints) are arbitrary.
func TestValidatePushTokenFormat(t *testing.T) {
	apns64 := strings.Repeat("a1", 32) // 64 hex chars
	cases := []struct {
		name     string
		platform string
		token    string
		ok       bool
	}{
		{"ios valid 64-hex", "ios", apns64, true},
		{"ios uppercase hex ok", "ios", strings.ToUpper(apns64), true},
		{"ios too short", "ios", "deadbeef", false},
		{"ios non-hex", "ios", strings.Repeat("z", 64), false},
		{"android valid long opaque", "android", strings.Repeat("xY9_:-", 8), true},
		{"android too short", "android", "short", false},
		{"web anything non-empty", "web", "https://push.example/abc", true},
		{"web empty rejected", "web", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePushTokenFormat(c.platform, c.token)
			if c.ok && err != nil {
				t.Fatalf("want valid, got error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("want invalid, got nil error")
			}
		})
	}
}

// Story 12.10 AC: PATCH body accepts categories[] + locale only; an
// unknown / non-allowed category is rejected.
func TestNormalizeCategories(t *testing.T) {
	got, err := normalizeCategories([]string{"job", "JOB", "library", "subscription", "system"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deduped + lowercased, order-stable.
	want := []string{"job", "library", "subscription", "system"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if _, err := normalizeCategories([]string{"job", "not-a-category"}); err == nil {
		t.Fatalf("expected rejection of unknown category")
	}
}
