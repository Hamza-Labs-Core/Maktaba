package componentseed

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

var ctx = context.Background()

// today is a home as most run today: every default, the TV slots on, the relay off.
func today() Facts {
	return Facts{Config: config.Config{}, Slots: true, Assets: map[string]bool{}, OperatorOff: map[string]bool{},
		Extensions: map[string]bool{}}
}

// T01: each rule of §2, both ways.
func TestPlanRules(t *testing.T) {
	cat := components.Default()
	cases := []struct {
		name string
		edit func(*Facts)
		id   string
		want string
	}{
		{"core always", func(f *Facts) {}, "core.postgres", "enabled"},
		{"core.pipeline always", func(f *Facts) {}, "core.pipeline", "enabled"},
		{"stage group on", func(f *Facts) {}, "youtube", "enabled"},
		{"stage group: one stage still on", func(f *Facts) {
			f.OperatorOff = map[string]bool{"youtube_sync": true, "youtube_fetch": true, "youtube_backfill": true}
		}, "youtube", "enabled"},
		{"stage group: every stage off by an operator", func(f *Facts) {
			f.OperatorOff = map[string]bool{"youtube_sync": true, "youtube_fetch": true, "youtube_backfill": true, "exact_dates": true}
		}, "youtube", "installed"},
		{"thumbnails off on the Queues page", func(f *Facts) {
			f.OperatorOff = map[string]bool{"thumbnail": true, "thumb_small": true, "chapter_thumbs": true}
		}, "media.thumbnails", "installed"},
		{"slots on", func(f *Facts) {}, "livetv.slots", "enabled"},
		{"slots on, every livetv stage off by an operator: livetv stays enabled", func(f *Facts) {
			f.OperatorOff = map[string]bool{"plan_channels": true, "write_channels": true}
		}, "livetv", "enabled"},
		{"slots off, every livetv stage off by an operator", func(f *Facts) {
			f.Slots = false
			f.OperatorOff = map[string]bool{"plan_channels": true, "write_channels": true}
		}, "livetv", "enabled"},
		{"slots on: in-process off", func(f *Facts) {}, "livetv.inprocess", "installed"},
		{"slots off", func(f *Facts) { f.Slots = false }, "livetv.slots", "installed"},
		{"slots off: in-process on", func(f *Facts) { f.Slots = false }, "livetv.inprocess", "enabled"},
		{"hdhomerun on", func(f *Facts) { f.HDHR = true }, "livetv.hdhomerun", "enabled"},
		{"hdhomerun off", func(f *Facts) {}, "livetv.hdhomerun", "installed"},
		{"llm process", func(f *Facts) { f.LLMProcess = true }, "llm", "enabled"},
		{"llm url", func(f *Facts) { f.Config.LLM.URL = "http://10.0.0.2:11434" }, "llm", "enabled"},
		{"no llm", func(f *Facts) {}, "llm", "installed"},
		{"plex on", func(f *Facts) { f.Extensions["plex"] = true }, "ext.plex", "enabled"},
		{"plex row off", func(f *Facts) { f.Extensions["plex"] = false }, "ext.plex", "installed"},
		{"no dizquetv row", func(f *Facts) {}, "ext.dizquetv", "installed"},
		{"cloudflare on", func(f *Facts) { f.Extensions["cloudflare_stream"] = true }, "ext.cloudflare_stream", "enabled"},
		{"relay wanted: link", func(f *Facts) { f.Relay = true }, "cloud.link", "enabled"},
		{"relay wanted: relay", func(f *Facts) { f.Relay = true }, "cloud.relay", "enabled"},
		{"no relay", func(f *Facts) {}, "cloud.link", "installed"},
		{"no relay: relay", func(f *Facts) {}, "cloud.relay", "installed"},
		{"mdns default", func(f *Facts) {}, "discovery.mdns", "enabled"},
		{"mdns off", func(f *Facts) { f.Config.Server.MDNS = "off" }, "discovery.mdns", "installed"},
		{"backups default", func(f *Facts) {}, "backups", "enabled"},
		{"backups off", func(f *Facts) { f.Config.Backups.Nightly = "off" }, "backups", "installed"},
		{"telemetry default", func(f *Facts) {}, "telemetry", "enabled"},
		{"telemetry off", func(f *Facts) { f.Config.Telemetry.Enabled = "off" }, "telemetry", "installed"},
		{"cluster always", func(f *Facts) {}, "cluster", "enabled"},
		{"queue health always", func(f *Facts) {}, "queue_health", "enabled"},
		{"analytics always", func(f *Facts) {}, "analytics", "enabled"},
		{"updates always", func(f *Facts) {}, "updates.auto", "enabled"},
		{"asset on disk", func(f *Facts) { f.Assets["model.whisper-turbo"] = true }, "model.whisper-turbo", "installed"},
		{"asset not on disk", func(f *Facts) {}, "model.whisper-turbo", "absent"},
		{"runtime on disk", func(f *Facts) { f.Assets["runtime.ml"] = true }, "runtime.ml", "installed"},
	}
	for _, c := range cases {
		f := today()
		c.edit(&f)
		got := Plan(cat, f)
		if got[c.id] != c.want {
			t.Errorf("%s: %s = %q, want %q", c.name, c.id, got[c.id], c.want)
		}
	}
	// every component has a want, and no asset is ever enabled
	f := today()
	for id := range f.Assets {
		delete(f.Assets, id)
	}
	plan := Plan(cat, f)
	for _, c := range cat.Components() {
		w, ok := plan[c.ID]
		if !ok {
			t.Errorf("%s has no want", c.ID)
		}
		if c.Kind == components.KindAsset && w == components.WantEnabled {
			t.Errorf("asset %s enabled", c.ID)
		}
		if c.Kind != components.KindAsset && w == components.WantAbsent {
			t.Errorf("%s seeded absent", c.ID)
		}
	}
}

// T02: [transcription] workers = -1 (transcribe in the main worker) is not imported.
func TestPlanTranscriptionWorkersNotImported(t *testing.T) {
	f := today()
	f.Config.Transcription.Workers = -1
	if w := Plan(components.Default(), f)["transcription"]; w != components.WantEnabled {
		t.Fatalf("transcription = %s", w)
	}
	if _, ok := Imported(f.Config)["transcription.workers"]; ok {
		t.Fatal("[transcription] workers imported")
	}
}

// T03: [llm] enabled = auto, no ollama in the payload, no url.
func TestPlanNoOllama(t *testing.T) {
	f := today()
	f.Config.LLM.Enabled = "auto"
	f.Assets = Assets(components.Default(), Disk{Config: dataDir(t), Getenv: func(string) string { return "" }})
	p := Plan(components.Default(), f)
	if p["llm"] != components.WantInstalled || p["runtime.ollama"] != components.WantAbsent {
		t.Fatalf("llm %s, runtime.ollama %s", p["llm"], p["runtime.ollama"])
	}
	if Imported(f.Config)["llm.enabled"] != "auto" || Imported(f.Config)["llm.url"] != "unset" {
		t.Fatalf("imported %v", Imported(f.Config))
	}
}

func dataDir(t *testing.T) config.Config {
	t.Helper()
	var c config.Config
	c.Server.DataDir = t.TempDir()
	return c
}

// Assets finds what today's code finds on disk.
func TestAssetsOnDisk(t *testing.T) {
	cfg := dataDir(t)
	state := cfg.StateDir()
	touch := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := "bin"
	exe := ""
	if runtime.GOOS == "windows" {
		bin, exe = "Scripts", ".exe"
	}
	touch(filepath.Join(state, "venv", bin, "python"+exe))
	touch(filepath.Join(state, "venv", bin, "yt-dlp"+exe))
	touch(filepath.Join(state, "models", "huggingface", "hub", "models--intfloat--multilingual-e5-small", "snapshots", "abc", "config.json"))
	touch(filepath.Join(state, "models", "ollama", "manifests", "registry.ollama.ai", "library", "qwen2.5", "7b"))
	got := Assets(components.Default(), Disk{Config: cfg, Platform: "linux-amd64", Ollama: true,
		Getenv: func(string) string { return "" }})
	for id, want := range map[string]bool{"runtime.ml": true, "model.refine": true, "tool.yt-dlp": true,
		"model.embedder": true, "runtime.ollama": true, "model.llm.qwen2_5-7b": true,
		"model.whisper-turbo": false, "model.cohere-arabic": false, "tool.ffmpeg": false} {
		if got[id] != want {
			t.Errorf("%s on disk = %v, want %v", id, got[id], want)
		}
	}
}

func TestLines(t *testing.T) {
	raw := []byte(`# a comment
listen = "x"
tv.slots = "on"

[server]
mdns = "off"   # no multicast

[tv]
# slots = "on"
slots = 'off'
[[media.roots]]
slots = "ignored"
[llm]
url = "http://h:1#x"
`)
	got := Lines(raw, Keys)
	if l := got["tv.slots"]; l.N != 3 || l.Value != "on" {
		t.Fatalf("tv.slots %+v (the first setting wins, as the line reported)", l)
	}
	if l := got["server.mdns"]; l.N != 6 || l.Value != "off" {
		t.Fatalf("server.mdns %+v", l)
	}
	if l := got["llm.url"]; l.N != 14 || l.Value != "http://h:1#x" {
		t.Fatalf("llm.url %+v", l)
	}
	if _, ok := got["backups.nightly"]; ok {
		t.Fatal("a key not in the file was found")
	}
}

// sqliteHome is a SQLite database with the tables the seed reads and writes (slot 0173's
// twin, and the older tables' columns it touches).
func sqliteHome(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "home.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	base := []string{
		`CREATE TABLE cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
			actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}', ts TEXT, actor_source TEXT, target_kind TEXT)`,
		`CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL DEFAULT '', subject_kinds TEXT NOT NULL DEFAULT '[]',
			pool TEXT NOT NULL DEFAULT 'cpu', lane TEXT NOT NULL DEFAULT 'media', lane_order INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 1, debounce_sec INTEGER NOT NULL DEFAULT 0, debounce_max_sec INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3, edges TEXT NOT NULL DEFAULT '[]', may_request TEXT NOT NULL DEFAULT '[]',
			entry_for TEXT NOT NULL DEFAULT '[]', legacy INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0,
			external_write INTEGER NOT NULL DEFAULT 0, write_mode TEXT NOT NULL DEFAULT 'auto',
			placement TEXT NOT NULL DEFAULT 'any', enabled INTEGER NOT NULL DEFAULT 1, disabled_reason TEXT)`,
		`CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL)`,
		`CREATE TABLE extensions (name TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE hdhr_device (id INTEGER PRIMARY KEY DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, s := range base {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", "0173_components.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	up = up[:strings.Index(up, "-- +goose Down")]
	for _, block := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
			if s := strings.TrimSpace(block[:i]); s != "" {
				if _, err := db.Exec(s); err != nil {
					t.Fatalf("%v\n%s", err, s)
				}
			}
		}
	}
	return db
}

func writeToml(t *testing.T, dir, body string) (string, config.Config) {
	t.Helper()
	p := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, cfg
}

func home(st *store.Store, path string, cfg config.Config, log *bytes.Buffer, env map[string]string) Home {
	f := today()
	f.Config = cfg
	f.Slots = cfg.TV.SlotsOn()
	return Home{Store: st, Facts: f, Release: "0.3.67", TomlPath: path, Log: log,
		Getenv: func(k string) string { return env[k] }}
}

// T04 (SQLite) and the AC: slots on, Plex on, every youtube stage off by an operator.
func TestHomeSeedsOnce(t *testing.T) {
	db := sqliteHome(t)
	for _, q := range []string{
		`INSERT INTO extensions (name, enabled) VALUES ('plex', 1), ('dizquetv', 0)`,
		`INSERT INTO hdhr_device (id, enabled) VALUES (1, 0)`,
		`INSERT INTO job_stages (stage, enabled, disabled_reason) VALUES ('youtube_sync', 0, 'operator'),
			('youtube_fetch', 0, 'operator'), ('youtube_backfill', 0, 'operator'), ('exact_dates', 0, 'operator'),
			('thumbnail', 0, 'not_configured')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	st := store.New(db)
	path, cfg := writeToml(t, t.TempDir(), "[tv]\nslots = \"on\"\n")
	var log bytes.Buffer
	h := home(st, path, cfg, &log, nil)
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Desired(ctx, store.Home)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"livetv.slots": "enabled", "ext.plex": "enabled", "livetv.inprocess": "installed",
		"youtube": "installed", "ext.dizquetv": "installed", "media.thumbnails": "enabled", "livetv.hdhomerun": "installed",
		"core.api": "enabled"} {
		if d[id].Want != want {
			t.Errorf("%s = %q, want %q", id, d[id].Want, want)
		}
	}
	if len(d) != len(components.Default().IDs()) {
		t.Errorf("%d rows for %d components", len(d), len(components.Default().IDs()))
	}
	m, _ := st.Meta(ctx)
	if !m.Seeded() || m.Bundle != "full" || m.SeededRelease != "0.3.67" || m.TomlImported["tv.slots"] != "on" {
		t.Fatalf("meta %+v", m)
	}
	var reason string
	_ = db.QueryRow(`SELECT disabled_reason FROM job_stages WHERE stage = 'youtube_fetch'`).Scan(&reason)
	if reason != "operator" {
		t.Fatalf("youtube_fetch reason %q", reason)
	}
	if !strings.Contains(log.String(), "desired state seeded") || strings.Contains(log.String(), "toml_key_superseded") {
		t.Fatalf("log: %s", log.String())
	}
	// a second boot: nothing more
	rev := m.Revision
	log.Reset()
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	m2, _ := st.Meta(ctx)
	if m2.Revision != rev || !m2.SeededAt.Equal(m.SeededAt) || strings.Contains(log.String(), "seeded") {
		t.Fatalf("a second boot seeded again: %+v %s", m2, log.String())
	}
}

// T06: seeded, then server.toml turned the slots off: the database wins, one
// toml_key_superseded with the line, the condition on the config part; nothing moves.
func TestTomlSuperseded(t *testing.T) {
	db := sqliteHome(t)
	st := store.New(db)
	dir := t.TempDir()
	path, cfg := writeToml(t, dir, "[tv]\nslots = \"on\"\n")
	var log bytes.Buffer
	if err := home(st, path, cfg, &log, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Meta(ctx)
	path, cfg = writeToml(t, dir, "# edited by the owner\n\n[tv]\nslots = \"off\"\n")
	log.Reset()
	if err := home(st, path, cfg, &log, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), "toml_key_superseded"); n != 1 {
		t.Fatalf("%d toml_key_superseded lines:\n%s", n, log.String())
	}
	if !strings.Contains(log.String(), "key=tv.slots") || !strings.Contains(log.String(), "line=4") ||
		!strings.Contains(log.String(), "component=livetv.slots") {
		t.Fatalf("log: %s", log.String())
	}
	if d, _ := st.DesiredOf(ctx, "livetv.slots"); d.Want != "enabled" || d.Generation != 1 {
		t.Fatalf("livetv.slots moved: %+v", d)
	}
	if m2, _ := st.Meta(ctx); m2.Revision != m.Revision {
		t.Fatalf("revision moved %d -> %d", m.Revision, m2.Revision)
	}
	rows, err := st.StatusOf(ctx, "livetv.slots")
	if err != nil || len(rows) != 1 {
		t.Fatalf("status rows %v %v", rows, err)
	}
	s := rows[0]
	if s.Part != Part || s.Reason() != "toml_superseded" || s.Phase != components.PhaseRunning {
		t.Fatalf("status %+v", s)
	}
	if s.Conditions[0].Params["line"] != float64(4) || s.Conditions[0].Params["value"] != "off" {
		t.Fatalf("condition params %v", s.Conditions[0].Params)
	}
	// put right in the file: the next boot clears the row
	path, cfg = writeToml(t, dir, "[tv]\nslots = \"on\"\n")
	if err := home(st, path, cfg, &log, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.StatusOf(ctx, "livetv.slots"); len(rows) != 0 {
		t.Fatalf("rows after the file was put right: %+v", rows)
	}
}

// A superseded [llm] url is reported as "set": the url may carry a login, and a finding
// goes to the log and to a status row.
func TestTomlSupersededHidesURL(t *testing.T) {
	raw := []byte("[llm]\nurl = \"http://user:pw@10.0.0.2:11434\"\n")
	var cfg config.Config
	cfg.LLM.URL = "http://user:pw@10.0.0.2:11434"
	found := Superseded(raw, cfg, map[string]store.Desired{"llm": {Want: components.WantInstalled}})
	if len(found) != 1 || found[0].Key != "llm.url" || found[0].Value != "set" {
		t.Fatalf("findings %+v", found)
	}
	var log bytes.Buffer
	LogSuperseded(&log, "server.toml", found)
	if strings.Contains(log.String(), "pw@") {
		t.Fatalf("log carries the url: %s", log.String())
	}
}

// AC: MAKTABA_UPDATE_DISABLE=1 at the seed: updates.auto still enabled, its status says
// env_override (and the variable); --no-mdns says boot_flag.
func TestEnvOverrideNotImported(t *testing.T) {
	db := sqliteHome(t)
	st := store.New(db)
	path, cfg := writeToml(t, t.TempDir(), "")
	var log bytes.Buffer
	h := home(st, path, cfg, &log, map[string]string{"MAKTABA_UPDATE_DISABLE": "1", "MAKTABA_CLUSTER_AGENT": "0"})
	h.NoMDNS = true
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"updates.auto", "cluster", "discovery.mdns"} {
		if d, _ := st.DesiredOf(ctx, id); d.Want != components.WantEnabled {
			t.Fatalf("%s = %q", id, d.Want)
		}
	}
	rows, _ := st.StatusOf(ctx, "updates.auto")
	if len(rows) != 1 || rows[0].Reason() != "env_override" || rows[0].Phase != components.PhaseDegraded ||
		rows[0].Conditions[0].Params["variable"] != "MAKTABA_UPDATE_DISABLE" {
		t.Fatalf("updates.auto status %+v", rows)
	}
	rows, _ = st.StatusOf(ctx, "discovery.mdns")
	if len(rows) != 1 || rows[0].Reason() != "boot_flag" {
		t.Fatalf("discovery.mdns status %+v", rows)
	}
}

// T07's plan: a machine whose server.toml has the slots off, its home on.
func TestNodeOverrides(t *testing.T) {
	home := map[string]store.Desired{"livetv.slots": {Want: "enabled"}, "livetv.inprocess": {Want: "installed"},
		"discovery.mdns": {Want: "enabled"}, "llm": {Want: "installed"}}
	var cfg config.Config
	cfg.TV.Slots = "off"
	got := NodeOverrides(cfg, false, home)
	if len(got) != 2 || got["livetv.slots"] != "installed" || got["livetv.inprocess"] != "enabled" {
		t.Fatalf("overrides %v", got)
	}
	cfg.TV.Slots = ""
	if got := NodeOverrides(cfg, false, home); len(got) != 0 {
		t.Fatalf("a machine like its home has overrides: %v", got)
	}
	if got := NodeOverrides(cfg, false, map[string]store.Desired{}); len(got) != 0 {
		t.Fatalf("overrides of components the home has no row for: %v", got)
	}
}

// A machine seeds once its home has, once (its marker).
func TestNodeRunOnce(t *testing.T) {
	db := sqliteHome(t)
	st := store.New(db)
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id, name) VALUES ('n1', 'n1')`); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.TV.Slots = "off"
	marker := filepath.Join(t.TempDir(), "state", "components-seeded.json")
	n := Node{Store: st, NodeID: "n1", Config: cfg, Marker: marker}
	if err := n.Run(ctx); err != ErrHomeNotSeeded {
		t.Fatalf("before the home seeded: %v", err)
	}
	path, hcfg := writeToml(t, t.TempDir(), "")
	if err := home(st, path, hcfg, &bytes.Buffer{}, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	d, _, _ := st.Desired(ctx, "n1")
	if d["livetv.slots"].Want != "installed" || d["livetv.inprocess"].Want != "enabled" {
		t.Fatalf("merged: %+v %+v", d["livetv.slots"], d["livetv.inprocess"])
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	// the marker: an admin's later change is never redone
	if _, err := db.Exec(`DELETE FROM component_overrides`); err != nil {
		t.Fatal(err)
	}
	if err := n.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var k int
	_ = db.QueryRow(`SELECT COUNT(*) FROM component_overrides`).Scan(&k)
	if k != 0 {
		t.Fatalf("%d overrides seeded again", k)
	}
}
