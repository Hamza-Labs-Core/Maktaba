package settings

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	in := map[string]any{
		"stt.openai.api_key": "sk-xxx",
		"normal.value":       42,
		"some.password":      "pw",
		"empty.token":        "",
	}
	out := redactSecrets(in)
	if out["stt.openai.api_key"] != "<redacted>" || out["stt.openai.api_key_present"] != true {
		t.Errorf("api_key not redacted: %v", out)
	}
	if out["normal.value"].(int) != 42 {
		t.Errorf("non-secret was modified")
	}
	if out["some.password"] != "<redacted>" {
		t.Errorf("password not redacted")
	}
	if out["empty.token_present"] != false {
		t.Errorf("empty token should be reported absent")
	}
}

func TestRedactSecrets_NeverEmitsValueLooking(t *testing.T) {
	in := map[string]any{
		"foo.token":  "shhh-1234",
		"bar.secret": "moresecret",
	}
	out := redactSecrets(in)
	for _, v := range out {
		if s, ok := v.(string); ok {
			if strings.Contains(s, "shhh") || strings.Contains(s, "moresecret") {
				t.Errorf("leaked secret in %q", s)
			}
		}
	}
}
