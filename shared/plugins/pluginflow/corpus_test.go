package pluginflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

const corpus = "../testdata/flows"

type caseMeta struct {
	Mode   string            `json:"mode"`
	States map[string]string `json:"states"`
}

type expect struct {
	Findings []Finding           `json:"findings"`
	Graph    Graph               `json:"graph"`
	Messages map[string][]string `json:"messages"`
}

// RunCase answers a corpus case the way the home ("home") or the store ("store") would.
func runCase(t *testing.T, dir string) expect {
	t.Helper()
	var meta caseMeta
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	manifests := map[string]map[string]any{}
	paths, _ := filepath.Glob(filepath.Join(dir, "plugins", "*.toml"))
	sort.Strings(paths)
	var ids []string
	for _, p := range paths {
		src, _ := os.ReadFile(p)
		m, errs := pluginmanifest.Validate(src, nil)
		if errs != nil {
			t.Fatalf("%s: %v", p, errs)
		}
		manifests[m["id"].(string)] = m
		ids = append(ids, m["id"].(string))
	}
	var findings []Finding
	var members []Member
	if meta.Mode == "store" {
		src, err := os.ReadFile(filepath.Join(dir, "flow.toml"))
		if err != nil {
			t.Fatal(err)
		}
		f, fs := ParseFlow(src)
		findings = append(findings, fs...)
		inFlow := map[string]bool{}
		for _, m := range f.Members {
			inFlow[m.ID] = true
		}
		members = append(members, f.Members...)
		for _, id := range ids {
			if inFlow[id] {
				findings = append(findings, MatchManifest(f, manifests[id])...)
			} else {
				members = append(members, FromManifest(manifests[id], ""))
			}
		}
		// the store's other plugins, reached only through the flow's publishers
		catalog := map[string]map[string]any{}
		cpaths, _ := filepath.Glob(filepath.Join(dir, "catalog", "*.toml"))
		for _, p := range cpaths {
			src, _ := os.ReadFile(p)
			m, errs := pluginmanifest.Validate(src, nil)
			if errs != nil {
				t.Fatalf("%s: %v", p, errs)
			}
			catalog[m["id"].(string)] = m
		}
		members = ResolveDepends(members, func(id, _ string) (Member, bool) {
			m, ok := catalog[id]
			if !ok {
				return Member{}, false
			}
			return FromManifest(m, ""), true
		})
	} else {
		for _, id := range ids {
			members = append(members, FromManifest(manifests[id], meta.States[id]))
		}
	}
	g, fs := Check(members, Options{Strict: meta.Mode == "store"})
	findings = append(findings, fs...)
	if findings == nil {
		findings = []Finding{}
	}
	out := expect{Findings: findings, Graph: g, Messages: map[string][]string{"en": {}, "ar": {}}}
	for _, f := range findings {
		out.Messages["en"] = append(out.Messages["en"], Message(f, "en"))
		out.Messages["ar"] = append(out.Messages["ar"], Message(f, "ar"))
	}
	return out
}

func encode(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.Bytes()
}

// T01 (go): every case of the flow corpus gives its expected findings, graph and words.
func TestCorpus(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join(corpus, "*"))
	if len(dirs) < 20 {
		t.Fatalf("only %d cases", len(dirs))
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			got := encode(runCase(t, dir))
			golden := filepath.Join(dir, "expect.json")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("differs from %s:\n%s", golden, got)
			}
		})
	}
}

// T08 (go): every code has English and Arabic words, and the placeholders are filled.
func TestMessages(t *testing.T) {
	codes := []string{CodeInvalid, CodeLimit, CodeDuplicate, CodeNotMember, CodeManifestMismatch, CodeCoreEdge,
		CodeForeignStage, CodeForeignEvent, CodeUnknownEvent, CodeSubjectMismatch, CodeMissingPlugin, CodeCycle,
		CodeTooDeep, "flow_unknown_key"}
	for _, c := range codes {
		f := Finding{Code: c, Plugin: "dev.example.a", Detail: "x", Path: []string{"a/b", "a/b"}}
		for _, lang := range []string{"en", "ar"} {
			m := Message(f, lang)
			if m == "" || strings.Contains(m, "{") {
				t.Errorf("%s %s: %q", c, lang, m)
			}
		}
		if Message(f, "ar") == Message(f, "en") {
			t.Errorf("%s: no Arabic", c)
		}
	}
}

// T09 (go): N22, the check over 100 plugins of 20 stages each in at most 50 ms.
func budgetMembers() []Member {
	var members []Member
	for p := 0; p < 100; p++ {
		id := fmt.Sprintf("dev.example.p%03d", p)
		m := Member{ID: id, Version: "1.0.0", Depends: map[string]string{}}
		for s := 0; s < 20; s++ {
			st := Stage{Name: fmt.Sprintf("s%02d", s), Subject: "video"}
			if s == 0 {
				st.Triggers = []string{"audio.ready"}
				if p > 0 {
					prev := fmt.Sprintf("dev.example.p%03d", p-1)
					st.Triggers = append(st.Triggers, "plugin:"+prev+"/p.done")
					m.Depends[prev] = ">=1.0.0"
				}
			} else {
				st.Triggers = []string{fmt.Sprintf("stage:s%02d", s-1)}
			}
			if s == 19 {
				st.Emits = []string{"p.done"}
			}
			m.Stages = append(m.Stages, st)
		}
		m.Events = []Event{{Name: "p.done", Subject: "video", Payload: "events/p.json"}}
		members = append(members, m)
	}
	return members
}

func TestBudget(t *testing.T) {
	members := budgetMembers()
	// N22 is the check's own time on a home: warm (not the first read of events.toml), the
	// best of five, and counted as this process's CPU time where the system gives it, so a
	// runner busy with other jobs does not time its own queue
	Check(members, Options{})
	best := time.Hour
	var fs []Finding
	for i := 0; i < 5; i++ {
		wall, cpu := time.Now(), cpuTime()
		_, fs = Check(members, Options{})
		took := time.Since(wall)
		if c := cpuTime(); c > 0 {
			took = c - cpu
		}
		best = min(best, took)
	}
	if best > 50*time.Millisecond {
		t.Fatalf("took %s", best)
	}
	if !HasRefusal(fs) {
		t.Fatal("a 2,000-stage chain must be too deep")
	}
}

func TestInRange(t *testing.T) {
	for _, c := range []struct {
		v, r string
		ok   bool
	}{{"1.4.0", ">=1.2.0, <2.0.0", true}, {"2.0.0", ">=1.2.0, <2.0.0", false}, {"2.0.0-beta.1", "<2.0.0", true},
		{"1.0.0", "=1.0.0", true}, {"1.0.1", ">1.0.0", true}, {"1.0.0", ">1.0.0", false}, {"10.0.0", ">=9.0.0", true}} {
		if InRange(c.v, c.r) != c.ok {
			t.Errorf("%s in %s: want %v", c.v, c.r, c.ok)
		}
	}
}
