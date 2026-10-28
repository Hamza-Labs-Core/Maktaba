package queues

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// The API enqueues Epic 47's stages at the pipeline's priorities: Go and
// Python must agree (pipeline engine/graph.py EPIC47_PRIORITIES and
// engine/reconcile.py SOURCE_REFRESH_PRIORITY).
func TestEpic47PrioritiesMatchThePipeline(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_pipeline", "engine", rel))
		if os.IsNotExist(err) {
			t.Skipf("pipeline/ is not in this checkout")
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	graph := read("graph.py")
	block := regexp.MustCompile(`(?s)EPIC47_PRIORITIES: dict\[str, int\] = \{(.*?)\n\}`).FindStringSubmatch(graph)
	if block == nil {
		t.Fatal("EPIC47_PRIORITIES not found in graph.py")
	}
	py := map[string]int{}
	for _, m := range regexp.MustCompile(`"(\w+)":\s*(\d+)`).FindAllStringSubmatch(block[1], -1) {
		n, _ := strconv.Atoi(m[2])
		py[m[1]] = n
	}
	if len(py) != len(Epic47Priorities) {
		t.Errorf("python has %v, go has %v", py, Epic47Priorities)
	}
	for k, v := range py {
		if Epic47Priorities[k] != v {
			t.Errorf("%s: python %d, go %d", k, v, Epic47Priorities[k])
		}
	}
	m := regexp.MustCompile(`(?m)^SOURCE_REFRESH_PRIORITY = (\d+)$`).FindStringSubmatch(read("reconcile.py"))
	if m == nil || m[1] != strconv.Itoa(SourceRefreshPriority) {
		t.Errorf("SOURCE_REFRESH_PRIORITY: python %v, go %d", m, SourceRefreshPriority)
	}
}

// Every cause the engine names (engine/causes.py _KNOWN) has its label here.
func TestEveryPipelineCauseHasALabel(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_pipeline", "engine", "causes.py"))
	if os.IsNotExist(err) {
		t.Skip("pipeline/ is not in this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)_KNOWN: .*?\n\)\n`).Find(b)
	causes := regexp.MustCompile(`(?m)^\s*\(\s*"(\w+)",`).FindAllSubmatch(block, -1)
	if len(causes) < 10 {
		t.Fatalf("only %d causes read from causes.py", len(causes))
	}
	for _, m := range causes {
		if _, ok := causeLabels[string(m[1])]; !ok {
			t.Errorf("cause %q of causes.py has no label in causes.go", m[1])
		}
	}
}

func TestEpic47Reasons(t *testing.T) {
	for sig, want := range map[string]string{
		"convert_verify:frames differ":   "convert_verify",
		"convert_swap:target exists":     "convert_failed",
		"youtube_rate_limited":           "youtube_rate_limited",
		"youtube_gone:three in a row":    "youtube_downloads_failing",
		"tartube_refused:pickle names x": "tartube",
		"keeper_unreadable:ffprobe":      "dedup_keeper_unreadable",
		"trash_size_mismatch:n bytes":    "trash_changed",
		"restore_refused:purged":         "restore_refused",
		"probe_failed:x":                 "",
	} {
		if got := epic47Reason(sig); got != want {
			t.Errorf("%s -> %q, want %q", sig, got, want)
		}
	}
	s := "convert_verify:frames differ"
	if r := itemReason(FailureItem{Status: StatusNeedsYou, State: "failed", Signature: &s}); r != "convert_verify" {
		t.Errorf("item reason %q", r)
	}
}

func TestSourceOptionsAreCheckedAsThePipelineReadsThem(t *testing.T) {
	out, errs := validateOptions(map[string]any{"max_height": 1080.0, "sub_langs": []any{"ar", "en"},
		"auto_subs": false, "audio_only": true, "cookies_from_browser": "chrome:Default", "limit_rate": "2M",
		"max_height_old": nil})
	if len(errs) != 0 || out["max_height"] != 1080 || out["sub_langs"] != "ar,en" || out["limit_rate"] != "2M" {
		t.Fatalf("good options: %v %v", out, errs)
	}
	for _, bad := range []map[string]any{{"max_height": 143.0}, {"max_height": 720.5}, {"sub_langs": "ar en"},
		{"sub_langs": []any{1.0}}, {"audio_only": "yes"}, {"cookies_from_browser": "x;y"}, {"limit_rate": "fast"},
		{"what": true}} {
		if _, errs := validateOptions(bad); len(errs) != 1 {
			t.Errorf("%v: %v", bad, errs)
		}
	}
	for f, ok := range map[string]bool{"Seerah": true, "Playlists/Seerah 2": true, "../x": false, "/abs": false,
		"a/../b": false, ".maktaba/trash": false, "a\\b": false} {
		if validFolder(f) != ok {
			t.Errorf("folder %q: %v", f, !ok)
		}
	}
}

func TestKeyset(t *testing.T) {
	at, id, ok := parseKeyset(makeKeyset(t0, "lib/abc"))
	if tt, isTime := at.(time.Time); !ok || id != "lib/abc" || !isTime || !tt.Equal(t0) {
		t.Fatalf("round trip: %v %q %v", at, id, ok)
	}
	for _, bad := range []string{"%%%", "abc", makeKeyset(t0, "")} {
		if _, _, ok := parseKeyset(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
