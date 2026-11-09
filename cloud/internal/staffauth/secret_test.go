package staffauth

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// MAKTABA_CLOUD_STAFF_SECRET_KEY is 32 bytes as hex or base64; anything else is refused.
func TestParseKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	for _, s := range []string{hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw), " " + hex.EncodeToString(raw) + "\n"} {
		got, err := ParseKey(s)
		if err != nil || string(got) != string(raw) {
			t.Errorf("ParseKey(%q) = %x, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "short", hex.EncodeToString(raw[:16]), strings.Repeat("z", 64)} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) accepted", s)
		}
	}
}
