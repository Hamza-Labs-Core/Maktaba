package llmserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// The state the supervisor serves (/llm, /processes: the System health page) never
// carries an [llm] url's login or api_key, in its URL or in an error quoting it.
func TestStatusHidesTheURLsCredentials(t *testing.T) {
	raw := "http://bob:s3cret@127.0.0.1:1/v1?api_key=k3y"
	m := NewManager(raw, []string{"qwen2.5:7b"}, false, false, nil)
	m.set(func(s *Status) {
		s.Error = `Get "http://bob:s3cret@127.0.0.1:1/v1?api_key=k3y/api/version": refused`
		s.Models[0].Error = "api_key=k3y refused"
	})
	st := m.Status()
	if st.URL != "http://…@127.0.0.1:1/v1?…" {
		t.Errorf("URL = %q", st.URL)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "k3y") {
		t.Errorf("served state carries a credential: %s", b)
	}
	if m.Client.URL != raw {
		t.Error("the client lost the URL it needs")
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:11434":                        "http://127.0.0.1:11434",
		"https://bob:s3cret@llm.example.net:8443/v1":    "https://…@llm.example.net:8443/v1",
		"https://llm.example.net/v1?api_key=s3cret&x=1": "https://llm.example.net/v1?…",
		"http://[::1:bad":                               "…",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
