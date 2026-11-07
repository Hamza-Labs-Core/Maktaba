package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #758: the lines a report of a plugin to its store carries are that plugin's own, scrubbed,
// and capped; nothing when the index is down.
func TestPluginLines(t *testing.T) {
	var asked string
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" { // a stray request: not the index's search
			http.NotFound(w, r)
			return
		}
		asked = r.URL.RawQuery
		lines := []string{
			`{"service":"plugin.dev.example.p","level":"warn","msg":"fetch https://home.lan/api?token=abc failed"}`,
			`{"service":"api","level":"warn","msg":"not the plugin's"}`,
			`{"service":"plugin.dev.example.other","msg":"another plugin's"}`,
		}
		for i := 0; i < 400; i++ {
			lines = append(lines, fmt.Sprintf(`{"service":"plugin.dev.example.p","msg":"line %d"}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"lines":[%s]}`, strings.Join(lines, ","))
	}))
	defer idx.Close()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	got := PluginLines(context.Background(), strings.TrimPrefix(idx.URL, "http://"), "dev.example.p", now)
	if len(got) != PluginLinesMax {
		t.Fatalf("%d lines, want the cap %d", len(got), PluginLinesMax)
	}
	for _, l := range got {
		var head struct {
			Service string `json:"service"`
		}
		_ = json.Unmarshal(l, &head)
		if head.Service != "plugin.dev.example.p" {
			t.Fatalf("a line not the plugin's: %s", l)
		}
	}
	if strings.Contains(string(got[0]), "token=abc") || !strings.Contains(string(got[0]), "token=[REDACTED]") {
		t.Fatalf("not scrubbed: %s", got[0])
	}
	if !strings.Contains(asked, "service=plugin.dev.example.p") || !strings.Contains(asked, "since=2026-11-06T12%3A00%3A00Z") {
		t.Fatalf("asked the index %q", asked)
	}
	if PluginLines(context.Background(), "", "dev.example.p", now) != nil {
		t.Fatal("no index: lines")
	}
	idx.Close()
	if PluginLines(context.Background(), strings.TrimPrefix(idx.URL, "http://"), "dev.example.p", now) != nil {
		t.Fatal("the index down: lines")
	}
}
