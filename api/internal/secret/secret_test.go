package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestValue_RedactsAcrossPrintingSurfaces(t *testing.T) {
	v := New("hunter2")
	if got := v.String(); got != RedactedString {
		t.Errorf("String() = %q, want %q", got, RedactedString)
	}
	if got := fmt.Sprintf("%v", v); got != RedactedString {
		t.Errorf("%%v = %q, want %q", got, RedactedString)
	}
	if got := fmt.Sprintf("%#v", v); got != RedactedString {
		t.Errorf("%%#v = %q, want %q", got, RedactedString)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// json.Marshal HTML-escapes <, >, &; both forms are valid JSON of
	// the redacted placeholder. Decode-and-compare avoids brittleness.
	var roundTrip string
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if roundTrip != RedactedString {
		t.Errorf("json roundtrip = %q, want %q", roundTrip, RedactedString)
	}
	if v.Reveal() != "hunter2" {
		t.Errorf("Reveal() = %q, want hunter2", v.Reveal())
	}
}

func TestValue_LoggerEmitsRedactedNotPlaintext(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)
	logger := slog.New(h)
	logger.Info("setup", "jwt_private_key", New("super-secret-rsa-pem"))
	out := buf.String()
	if strings.Contains(out, "super-secret-rsa-pem") {
		t.Errorf("plaintext leaked into log output: %s", out)
	}
	if !strings.Contains(out, RedactedString) {
		t.Errorf("expected %q in log, got: %s", RedactedString, out)
	}
}

func TestFromEnvOrFile_EnvBeatsFile(t *testing.T) {
	t.Setenv("MAKTABA_TEST_SECRET", "from-env")
	got := FromEnvOrFile("MAKTABA_TEST_SECRET", "from-file", nil)
	if got.Reveal() != "from-env" {
		t.Errorf("env should win, got %q", got.Reveal())
	}
}

func TestFromEnvOrFile_FallsBackToFile(t *testing.T) {
	t.Setenv("MAKTABA_TEST_SECRET", "")
	got := FromEnvOrFile("MAKTABA_TEST_SECRET", "from-file", nil)
	if got.Reveal() != "from-file" {
		t.Errorf("file should be used when env empty, got %q", got.Reveal())
	}
}

func TestFromEnvOrFile_BothEmpty(t *testing.T) {
	t.Setenv("MAKTABA_TEST_SECRET", "")
	got := FromEnvOrFile("MAKTABA_TEST_SECRET", "", nil)
	if got.Present() {
		t.Errorf("expected absent, got Present()=true")
	}
}

type sampleSettings struct {
	Public        string `json:"public"`
	JWTPrivateKey string `json:"jwt_private_key" secret:"true"`
	UserToken     string `json:"user_token"` // matches by name
	NotSecret     string `json:"my_token" notsecret:"true"`
	APIPort       int    `json:"api_port"`
}

func TestRedactSettings_TagsAndNameMatching(t *testing.T) {
	s := sampleSettings{
		Public:        "hello",
		JWTPrivateKey: "rsa-pem-bytes",
		UserToken:     "tok-12345",
		NotSecret:     "actually-public",
		APIPort:       8080,
	}
	got := RedactSettings(&s)

	if got["public"] != "hello" {
		t.Errorf("public should pass through, got %v", got["public"])
	}
	if got["jwt_private_key"] != RedactedString {
		t.Errorf("jwt_private_key should be redacted, got %v", got["jwt_private_key"])
	}
	if got["jwt_private_key_present"] != true {
		t.Errorf("jwt_private_key_present should be true, got %v", got["jwt_private_key_present"])
	}
	if got["user_token"] != RedactedString {
		t.Errorf("user_token should be redacted by name match, got %v", got["user_token"])
	}
	// `my_token` matches the sensitive substring "token" but has notsecret:"true".
	if got["my_token"] != "actually-public" {
		t.Errorf("my_token should be opted out, got %v", got["my_token"])
	}
	if got["api_port"] != 8080 {
		t.Errorf("api_port should pass through, got %v", got["api_port"])
	}

	// JSON of the redacted result must not contain plaintext.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, secret := range []string{"rsa-pem-bytes", "tok-12345"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("plaintext %q leaked into JSON: %s", secret, b)
		}
	}
}

func TestRedactURL_StripsSig(t *testing.T) {
	cases := map[string]string{
		"https://x/y?sig=abc":              "https://x/y?sig=" + RedactedString,
		"https://x/y?foo=1&sig=abc":        "https://x/y?foo=1&sig=" + RedactedString,
		"https://x/y?sig=abc&foo=1":        "https://x/y?sig=" + RedactedString + "&foo=1",
		"https://x/y?token=xyz":            "https://x/y?token=" + RedactedString,
		"https://x/y?access_token=xyz&z=1": "https://x/y?access_token=" + RedactedString + "&z=1",
		"https://x/y":                      "https://x/y",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSensitiveHeader(t *testing.T) {
	if !IsSensitiveHeader("authorization") {
		t.Error("authorization should be sensitive")
	}
	if !IsSensitiveHeader("Cookie") {
		t.Error("Cookie should be sensitive")
	}
	if IsSensitiveHeader("Content-Type") {
		t.Error("Content-Type should not be sensitive")
	}
}
