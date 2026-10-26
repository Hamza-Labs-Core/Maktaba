package devmigrate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeSys answers commands from a table keyed by the command line ("" output:
// the command fails, like pgrep with no match).
func fakeSys(t *testing.T, home string, env map[string]string, answers map[string]string) Sys {
	t.Helper()
	return Sys{
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			line := strings.Join(append([]string{name}, args...), " ")
			out, ok := answers[line]
			if !ok || out == "" {
				return exec.CommandContext(ctx, "false")
			}
			return exec.CommandContext(ctx, "printf", "%s", out)
		},
		Getenv: func(k string) string { return env[k] },
		Home:   home,
	}
}

func TestParseEnv(t *testing.T) {
	got := parseEnv("PWD=/a/b MAKTABA_PATH_MAP=/Volumes/Originals/=/Users/me/nas/ NAME=two words X=")
	want := map[string]string{"PWD": "/a/b", "MAKTABA_PATH_MAP": "/Volumes/Originals/=/Users/me/nas/", "NAME": "two words", "X": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEnv = %v, want %v", got, want)
	}
}

func TestFindStackAndSettings(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "src", "maktaba")
	if err := os.MkdirAll(filepath.Join(repo, "tools", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "dev", "stack.sh"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	main := "uv run --all-extras python -m maktaba_pipeline run --database-url postgresql://maktaba:maktaba@localhost:5432/maktaba --worker-id mac-main-1 --path-map /Volumes/Originals/=/Users/me/nas/ --grpc-addr 127.0.0.1:50051 --exclude-stages transcribe"
	stt := func(k string) string {
		return "python -m maktaba_pipeline run --database-url postgresql://maktaba:maktaba@localhost:5432/maktaba --worker-id mac-stt-" + k + "-1 --path-map /Volumes/Originals/=/Users/me/nas/ --stages transcribe"
	}
	pwd := " PWD=" + filepath.Join(repo, "pipeline")
	answers := map[string]string{
		"pgrep -f [/]api serve":                    "11\n",
		"pgrep -f [/]streaming serve":              "12\n",
		"pgrep -if python -m maktaba_pipeline run": "21\n22\n23\n24\n",
		"ps -ww -o command= -p 11":                 "/cache/api serve\n",
		"ps -wwE -o command= -p 11":                "/cache/api serve PWD=" + filepath.Join(repo, "api") + " MAKTABA_STREAM_SIGNING_KEY=/k/stream.key\n",
		"ps -ww -o command= -p 12":                 "/tmp/exe/streaming serve\n",
		"ps -wwE -o command= -p 12":                "/tmp/exe/streaming serve PWD=" + filepath.Join(repo, "streaming") + " MAKTABA_CACHE_ROOT=/c/streaming\n",
		"ps -ww -o command= -p 21":                 main + "\n",
		"ps -wwE -o command= -p 21":                main + pwd + " MAKTABA_STT_AUDIO_HOURS=12 MAKTABA_CHROMA_DIR=/x/chroma\n",
		"ps -ww -o command= -p 22":                 stt("1") + "\n",
		"ps -wwE -o command= -p 22":                stt("1") + pwd + " MAKTABA_STT_REFINE=0 MAKTABA_STT_SLOT=stt-1\n",
		"ps -ww -o command= -p 23":                 stt("2") + "\n",
		"ps -wwE -o command= -p 23":                stt("2") + pwd + "\n",
		"ps -ww -o command= -p 24":                 "uv run --all-extras " + stt("2") + "\n", // uv's parent of 23
		"ps -wwE -o command= -p 24":                "uv run --all-extras " + stt("2") + pwd + "\n",
	}
	sys := fakeSys(t, home, map[string]string{"MAKTABA_STT_GPU_GB": "30"}, answers)
	st := FindStack(context.Background(), sys, "")
	if st.Repo != repo {
		t.Fatalf("repo = %q, want %q", st.Repo, repo)
	}
	if got := st.Summary(); got != "api, streaming, main worker, 2 transcription workers" {
		t.Fatalf("summary = %q", got)
	}
	s := ReadSettings(sys, st)
	want := Settings{
		DatabaseURL: "postgresql://maktaba:maktaba@localhost:5432/maktaba",
		PathMap:     "/Volumes/Originals/=/Users/me/nas/",
		Workers:     2,
		AudioHours:  12, GPUGB: 30, GPUCacheGB: 2, MinFreePct: 25,
		Refine:     "off",
		SecretsKey: filepath.Join(home, ".config", "maktaba", "secrets.key"),
		StreamKey:  "/k/stream.key",
		ChromaDir:  "/x/chroma",
		CacheRoot:  "/c/streaming",
		HFHome:     filepath.Join(home, ".cache", "huggingface"),
		CamelData:  filepath.Join(home, ".camel_tools"),
		// the Homebrew Ollama's models (the LLM stages' model)
		OllamaModels: filepath.Join(home, ".ollama", "models"),
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("settings =\n%+v\nwant\n%+v", s, want)
	}
	if u, db, err := s.DB(); err != nil || u != "maktaba" || db != "maktaba" {
		t.Fatalf("DB() = %q %q %v", u, db, err)
	}
}

func TestStoppedStackDefaults(t *testing.T) {
	home := t.TempDir()
	sys := fakeSys(t, home, map[string]string{"XDG_CONFIG_HOME": "/xdg"}, nil)
	st := FindStack(context.Background(), sys, "")
	if len(st.Procs) != 0 || st.Summary() != "nothing (the stack is stopped)" {
		t.Fatalf("stack = %+v", st)
	}
	s := ReadSettings(sys, st)
	if s.PathMap != "/Volumes/Originals/="+home+"/nas-nfs/" || s.AudioHours != 16 || s.GPUGB != 20 || s.Workers != 0 {
		t.Fatalf("defaults = %+v", s)
	}
	if s.SecretsKey != "/xdg/maktaba/secrets.key" {
		t.Fatalf("secrets key = %q", s.SecretsKey)
	}
}

func TestHubRepos(t *testing.T) {
	s := Settings{Model: "/local/whisper", RefineModel: "a/b"}
	if got := s.HubRepos(); !reflect.DeepEqual(got, []string{"a/b", defaultEmbedder}) {
		t.Fatalf("HubRepos = %v", got)
	}
	if got := (Settings{}).HubRepos(); !reflect.DeepEqual(got, []string{defaultWhisper, defaultRefine, defaultEmbedder}) {
		t.Fatalf("default HubRepos = %v", got)
	}
}

func TestApplyNeverWritesTheDatabaseURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.toml")
	cfg := config.Defaults() // carries the sqlite URL
	s := Settings{PathMap: "/a/=/b/", Workers: 2, AudioHours: 16, GPUGB: 20, GPUCacheGB: 2, MinFreePct: 25, Refine: "off"}
	s.Apply(&cfg)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	got, _, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DatabaseURLSet {
		t.Fatal("server.toml sets [database] url: the bundled Postgres would not be used")
	}
	tr := got.Transcription
	if got.Media.PathMap != "/a/=/b/" || tr.Workers != 2 || tr.AudioHours != 16 || tr.GPUGB != 20 || tr.Refine != "off" {
		t.Fatalf("loaded %+v %+v", got.Media, tr)
	}
	if lines := s.TOMLLines(); len(lines) != 4 || !strings.Contains(lines[3], `refine = "off"`) {
		t.Fatalf("TOMLLines = %q", lines)
	}
}

func TestItemsCopy(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	keys := filepath.Join(state, "keys")
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := ReadSettings(fakeSys(t, home, nil, nil), Stack{})
	write(s.SecretsKey, "secret")
	write(filepath.Join(s.ChromaDir, "chroma.sqlite3"), "index")
	repo := filepath.Join(s.HFHome, "hub", "models--intfloat--multilingual-e5-small")
	write(filepath.Join(repo, "blobs", "abc"), "weights")
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", "r1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../blobs/abc", filepath.Join(repo, "snapshots", "r1", "model.bin")); err != nil {
		t.Fatal(err)
	}
	// the dev setup's Ollama models; the app's server already made its (empty)
	// folders before the move: the copy takes their place
	write(filepath.Join(s.OllamaModels, "manifests", "registry.ollama.ai", "library", "qwen2.5", "7b"), "manifest")
	write(filepath.Join(s.OllamaModels, "blobs", "sha256-1"), "gguf")
	if err := os.MkdirAll(filepath.Join(state, "models", "ollama", "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	items := Items(s, state, keys)
	byWhat := map[string]Item{}
	for _, it := range items {
		byWhat[it.What] = it
	}
	if !byWhat["secrets key"].Exists || byWhat["stream signing key"].Exists || byWhat["search index (Chroma)"].Size != 5 {
		t.Fatalf("items = %+v", items)
	}
	for _, it := range items {
		if err := it.Copy(false); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(keys, "secrets.key")); string(b) != "secret" {
		t.Fatalf("secrets.key = %q", b)
	}
	link := filepath.Join(state, "models", "huggingface", "hub", "models--intfloat--multilingual-e5-small", "snapshots", "r1", "model.bin")
	if b, err := os.ReadFile(link); err != nil || string(b) != "weights" {
		t.Fatalf("model through its link = %q, %v", b, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the snapshot link is not a link: %v", err)
	}
	if it := byWhat["language models (Ollama)"]; !it.Exists || it.Done || it.Size != int64(len("manifest")+len("gguf")) {
		t.Fatalf("ollama item = %+v", it)
	}
	if b, err := os.ReadFile(filepath.Join(state, "models", "ollama", "blobs", "sha256-1")); err != nil || string(b) != "gguf" {
		t.Fatalf("ollama blob in the app = %q, %v", b, err)
	}
	// again: everything is there, nothing changes
	for _, it := range Items(s, state, keys) {
		if it.Exists && !it.Done {
			t.Fatalf("%s not marked done", it.What)
		}
		if err := it.Copy(false); err != nil {
			t.Fatal(err)
		}
	}
	// a move made again replaces the folders (not the keys), atomically
	write(filepath.Join(s.ChromaDir, "chroma.sqlite3"), "newer index")
	for _, it := range Items(s, state, keys) {
		if err := it.Copy(true); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(state, "chroma", "chroma.sqlite3")); string(b) != "newer index" {
		t.Fatalf("chroma after a replace = %q", b)
	}
	if _, err := os.Stat(filepath.Join(state, "chroma.old")); err == nil {
		t.Fatal("chroma.old left behind")
	}
	// a key that is a link is copied as the file
	keyLink := filepath.Join(home, "linked.key")
	if err := os.Symlink(s.SecretsKey, keyLink); err != nil {
		t.Fatal(err)
	}
	ki := Item{What: "k", From: keyLink, To: filepath.Join(keys, "linked.key"), Key: true}
	ki.stat()
	if err := ki.Copy(false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(ki.To); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("linked key copied as a link: %v", err)
	}

	// a different key in the app's place is never replaced
	write(filepath.Join(keys, "secrets.key"), "another")
	for _, it := range Items(s, state, keys) {
		if it.What == "secrets key" {
			if err := it.Copy(false); err == nil || !strings.Contains(err.Error(), "never replaced") {
				t.Fatalf("copy over a different key: %v", err)
			}
		}
	}
}

func TestCopyTreeFallback(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "a"))
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("copied file: %v %v", fi, err)
	}
	if runtime.GOOS == "darwin" && !sameVolume(src, dst) {
		t.Log("temp dirs on different volumes")
	}
}

func TestReadFactsAndCompare(t *testing.T) {
	answers := map[string]string{}
	q := func(sql string) (string, error) {
		switch {
		case strings.Contains(sql, "FROM pg_class"):
			return "videos,transcripts,goose_db_version,instance_identity,streaming_hosts", nil
		case strings.HasPrefix(sql, "SELECT concat_ws"):
			if strings.Contains(sql, "FROM libraries") {
				t.Errorf("counts a table that does not exist: %s", sql)
			}
			return "videos=10 transcripts=3 streaming_hosts=2 goose=141 size=4096", nil
		case strings.Contains(sql, "instance_identity"):
			return "host-1", nil
		case strings.Contains(sql, "role = 'node'"):
			return "PC", nil
		case strings.Contains(sql, "FROM pg_roles"):
			return "maktaba_node_aa,maktaba_worker", nil
		case strings.Contains(sql, "pg_auth_members"):
			return "maktaba_node_aa>maktaba_worker", nil
		case strings.Contains(sql, "pg_extension"):
			return "pg_trgm,plpgsql", nil
		}
		return answers[sql], nil
	}
	f, err := ReadFacts(q)
	if err != nil {
		t.Fatal(err)
	}
	want := Facts{Counts: map[string]int64{"videos": 10, "transcripts": 3, "streaming_hosts": 2, "goose": 141}, Size: 4096,
		HostID: "host-1", Nodes: []string{"PC"}, Roles: []string{"maktaba_node_aa", "maktaba_worker"},
		Members: [][2]string{{"maktaba_node_aa", "maktaba_worker"}}, Extensions: []string{"pg_trgm", "plpgsql"}}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("facts =\n%+v\nwant\n%+v", f, want)
	}
	if u := f.Unrestorable(); u != nil {
		t.Fatalf("unrestorable = %q", u)
	}
	f2 := f
	f2.Extensions, f2.Roles = []string{"vector", "postgis"}, []string{"Bad-Name"}
	if u := f2.Unrestorable(); !reflect.DeepEqual(u, []string{"the extension postgis", "the role Bad-Name"}) {
		t.Fatalf("unrestorable = %q", u)
	}
	if got := f.MachineRoles(); !reflect.DeepEqual(got, []string{"maktaba_node_aa"}) {
		t.Fatalf("machine roles = %v", got)
	}
	if err := Compare(f.Counts, map[string]int64{"videos": 10, "transcripts": 3, "streaming_hosts": 2, "goose": 141}, true); err != nil {
		t.Fatal(err)
	}
	if err := Compare(f.Counts, map[string]int64{"videos": 11, "transcripts": 3, "streaming_hosts": 2, "goose": 141}, true); err == nil {
		t.Fatal("exact compare took a different count")
	}
	if err := Compare(f.Counts, map[string]int64{"videos": 11, "transcripts": 3, "streaming_hosts": 2, "goose": 142}, false); err != nil {
		t.Fatalf("grown counts: %v", err)
	}
	if err := Compare(f.Counts, map[string]int64{"videos": 9, "transcripts": 3, "streaming_hosts": 2}, false); err == nil ||
		!strings.Contains(err.Error(), "goose: missing") || !strings.Contains(err.Error(), "videos: 9, expected 10") {
		t.Fatalf("missing rows: %v", err)
	}
}

func TestFinalizeNeedsAVerifiedMove(t *testing.T) {
	state := t.TempDir()
	var ran []string
	sys := Sys{
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			line := strings.Join(append([]string{name}, args...), " ")
			ran = append(ran, line)
			if line == "docker inspect maktaba-postgres-1" {
				return exec.CommandContext(ctx, "printf", "%s", `[{"State":{"Running":false},"Config":{"Image":"postgres:16"},"Mounts":[{"Type":"volume","Name":"maktaba_pg_data","Destination":"/var/lib/postgresql/data"}]}]`)
			}
			return exec.CommandContext(ctx, "true")
		},
		Getenv: func(string) string { return "" },
		Home:   t.TempDir(),
	}
	var out strings.Builder
	checked := 0
	m := &Migration{Sys: sys, Target: Target{State: state, Keys: filepath.Join(state, "keys")}, Out: &out,
		check: func(context.Context, Marker) error { checked++; return nil }}
	yes := func(string) bool { return true }
	if err := m.Finalize(context.Background(), yes); err == nil || !strings.Contains(err.Error(), "no move") {
		t.Fatalf("finalize with no move: %v", err)
	}
	dump := filepath.Join(state, "from-dev.dump")
	mk := Marker{Stage: "migrated", Container: "maktaba-postgres-1", Volume: "maktaba_pg_data", Dump: dump}
	if err := m.writeMarker(mk); err != nil {
		t.Fatal(err)
	}
	if err := m.Finalize(context.Background(), yes); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("finalize before verify: %v", err)
	}
	mk.Stage = "verified"
	if err := m.writeMarker(mk); err != nil {
		t.Fatal(err)
	}
	if err := m.Finalize(context.Background(), yes); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("finalize without the dump: %v", err)
	}
	if err := os.WriteFile(dump, []byte("PGDMP"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.check = func(context.Context, Marker) error { return errors.New("the API does not answer") }
	if err := m.Finalize(context.Background(), yes); err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("finalize with the app down: %v", err)
	}
	m.check = func(context.Context, Marker) error { checked++; return nil }
	if err := m.Finalize(context.Background(), func(string) bool { return false }); err == nil {
		t.Fatal("finalize went ahead unconfirmed")
	}
	for _, l := range ran {
		if strings.HasPrefix(l, "docker rm") || strings.HasPrefix(l, "docker volume rm") {
			t.Fatalf("deleted unconfirmed: %s", l)
		}
	}
	if err := m.Finalize(context.Background(), yes); err != nil {
		t.Fatal(err)
	}
	if !contains(ran, "docker rm maktaba-postgres-1") || !contains(ran, "docker volume rm maktaba_pg_data") || checked != 2 {
		t.Fatalf("ran %q, checked %d", ran, checked)
	}
	got, err := m.readMarker()
	if err != nil || got.Stage != "finalized" || got.FinalizedAt == nil {
		t.Fatalf("marker = %+v, %v", got, err)
	}
	if err := m.Verify(context.Background(), time.Second); err == nil || !strings.Contains(err.Error(), "finalized") {
		t.Fatalf("verify after finalize: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestHuman(t *testing.T) {
	for n, want := range map[int64]string{999: "999 B", 1500: "1.5 kB", 11_900_000_000: "11.9 GB"} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}
