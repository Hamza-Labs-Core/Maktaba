package tvremote_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// The Worker (web/packages/tvsite/src/homeauth.ts) checks the home's posts
// with the same HMAC as SignHome: this fixture holds one signed post, which
// homeauth.test.ts verifies. Regenerate with MAKTABA_UPDATE_PARITY=1.
func TestHomeSignatureParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "web", "packages", "tvsite", "src", "testdata", "homeauth.json")
	type fixture struct {
		Note   string `json:"_note"`
		Secret string `json:"secret"`
		Method string `json:"method"`
		Path   string `json:"path"`
		TS     int64  `json:"ts"`
		Body   string `json:"body"`
		Sig    string `json:"sig"`
	}
	fx := fixture{Note: "TEST-ONLY secret; see api/internal/tvremote/homeauth_parity_test.go",
		Secret: "4f1c2a9e8b7d6c5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a",
		Method: "POST", Path: tvremote.SiteStatePath, TS: 1_793_000_000_000,
		Body: `{"schema":1,"at":"2026-10-26T12:00:00Z","customer_code":"abc123xyz","channels":[]}`}
	fx.Sig = tvremote.SignHome([]byte(fx.Secret), fx.Method, fx.Path, fx.TS, []byte(fx.Body))
	if os.Getenv("MAKTABA_UPDATE_PARITY") == "1" {
		b, _ := json.MarshalIndent(fx, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got fixture
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != fx {
		t.Error("SignHome changed: regenerate the fixture (MAKTABA_UPDATE_PARITY=1) and update homeauth.ts")
	}
}
