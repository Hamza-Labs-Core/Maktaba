package log

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactionMasksKeysBySuffix(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	logger.Info("settings",
		"cf_api_token", "t1",
		"JWT_Private_Key", "k1",
		"smtp_password", "p1",
		"token_ttl_minutes", 15,
		"signing_key_id", "kid-1",
	)
	got := decodeOne(t, buf.Bytes())
	for _, k := range []string{"cf_api_token", "JWT_Private_Key", "smtp_password"} {
		if got[k] != redactedValue {
			t.Errorf("%s not redacted: %v", k, got[k])
		}
	}
	if got["token_ttl_minutes"] != float64(15) || got["signing_key_id"] != "kid-1" {
		t.Errorf("non-secret keys must pass through: %v", got)
	}
}

func TestRedactionMasksURLCredentialsInValues(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	uerr := &url.Error{Op: "Get", URL: "http://plex.lan:32400/library/sections?X-Plex-Token=abc123&type=1",
		Err: errors.New("dial tcp: connection refused")}
	logger.Warn("plex unreachable",
		"err", uerr,
		"url", "https://cdn.example/v.m3u8?sig=deadbeef&exp=1",
		"detail", "a=b without a url",
	)
	line := buf.String()
	for _, secret := range []string{"abc123", "deadbeef"} {
		if strings.Contains(line, secret) {
			t.Fatalf("a credential reached the log: %s", line)
		}
	}
	got := decodeOne(t, buf.Bytes())
	if s, _ := got["err"].(string); !strings.Contains(s, "plex.lan:32400/library/sections") ||
		!strings.Contains(s, "type=1") || !strings.Contains(s, "connection refused") {
		t.Errorf("the rest of the error must stay: %v", got["err"])
	}
	if got["detail"] != "a=b without a url" {
		t.Errorf("text without a credential must pass through: %v", got["detail"])
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"/v/1.m3u8?sig=abc&exp=2":  "/v/1.m3u8?sig=" + redactedValue + "&exp=2",
		"/a?b=1&api_key=zz":        "/a?b=1&api_key=" + redactedValue,
		"/no/query":                "/no/query",
		"/a?TOKEN=zz#frag":         "/a?TOKEN=" + redactedValue + "#frag",
		"keyframe=3 (not a query)": "keyframe=3 (not a query)",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
