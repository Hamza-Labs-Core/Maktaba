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
