package supervisor

import (
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// Story 54.23 T02: a runner's environment has no database address, no secret and no
// token, and says where its sockets and areas are.
func TestRunnerEnvHasNoDatabaseNorSecrets(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.DataDir = t.TempDir()
	base := []string{"PATH=/usr/bin", "HOME=/home/x", "DATABASE_URL=postgres://u:p@h/db", "MAKTABA_HOME_SECRETS=/s",
		"MAKTABA_SUPERVISOR_TOKEN_FILE=/t", "MAKTABA_PLEX_TOKEN=abc", "MAKTABA_CLOUD_KEY=k", "PGPASSWORD=p",
		"MAKTABA_CLUSTER_PG_CA=/ca", "SOME_API_TOKEN=x", "MAKTABA_DATA_DIR=/old"}
	env := RunnerEnv(cfg, base)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, k := range []string{"DATABASE_URL", "MAKTABA_HOME_SECRETS", "MAKTABA_SUPERVISOR_TOKEN_FILE", "MAKTABA_PLEX_TOKEN",
		"MAKTABA_CLOUD_KEY", "PGPASSWORD", "MAKTABA_CLUSTER_PG_CA", "SOME_API_TOKEN"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s reached the runner", k)
		}
	}
	if got["PATH"] != "/usr/bin" || got["MAKTABA_RUNNER_DIR"] == "" || got[EnvPluginAPISocket] == "" ||
		got["MAKTABA_DATA_DIR"] != cfg.Server.DataDir || !strings.Contains(got["MAKTABA_RUNNER_AREAS"], "data.thumbnails") {
		t.Fatalf("env %v", got)
	}
}

// No first-party plugin in this release: no runner is built and none is needed.
func TestNoRunnerWithoutFirstPartyPlugins(t *testing.T) {
	was := sharedplugins.FirstParty
	sharedplugins.FirstParty = []byte(`{"plugins":[]}`)
	t.Cleanup(func() { sharedplugins.FirstParty = was })
	cfg := config.Config{}
	cfg.Database.URL = "postgres://h/db"
	cfg.Server.DataDir = t.TempDir()
	ps, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil || len(ps) != 0 {
		t.Fatalf("%d runners, %v", len(ps), err)
	}
	if runtime.GOOS == "windows" {
		return // no runner on Windows (RunnersHere), and no /tmp socket folder
	}
	longCfg := config.Config{}
	longCfg.Server.DataDir = "/" + strings.Repeat("d", 120)
	if long := RunnerDir(longCfg); !strings.HasPrefix(long, "/tmp/maktaba-r-") {
		t.Fatalf("a long data folder: %s", long)
	}
}

// Wave 1 on (story 54.28): this release's plugins name the light group, so a Postgres home
// builds its runner (dormant, on demand), and a SQLite one none. Wave 3 (story 54.30): an
// ml runner per transcription worker and the index runner, not dormant (their components',
// transcription and search.semantic, start them).
func TestTheReleaseBuildsTheRunnersItsPluginsName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no runner on Windows")
	}
	cfg := config.Config{}
	cfg.Database.URL = "postgres://h/db"
	cfg.Server.DataDir = t.TempDir()
	ps, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range ps {
		if p.Role != RolePluginRunner {
			t.Fatalf("%+v", p)
		}
		shared := containsString(RunnerGroups, p.Instance)
		if p.Dormant != shared {
			t.Fatalf("%s: dormant %v", p.Instance, p.Dormant)
		}
		got[p.Instance] = true
	}
	if !got["light"] || !got["fp"] || !got["index"] {
		t.Fatalf("runners %v", got)
	}
	ml := 0
	for name := range got {
		if name == GroupML || strings.HasPrefix(name, GroupML+"-") {
			ml++
		}
	}
	if want := max(PlanFor(Options{Config: cfg}).Workers, 1); ml != want {
		t.Fatalf("%d ml runners for %d transcription workers: %v", ml, want, got)
	}
	cfg.Database.URL = "sqlite:///x.db"
	if ps, _ := BuildPluginRunners(Options{Config: cfg}); len(ps) != 0 {
		t.Fatalf("a SQLite home built %d runners", len(ps))
	}
}

// An ml runner has its worker's slot and knobs, and no network for a model.
func TestAnMLRunnerHasItsWorkersSlotAndIsOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no runner on Windows")
	}
	cfg := config.Config{}
	cfg.Database.URL = "postgres://h/db"
	cfg.Server.DataDir = t.TempDir()
	ps, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		env := map[string]string{}
		for _, kv := range p.Cmd.Env {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		if env["HF_HUB_OFFLINE"] != "1" || env["TRANSFORMERS_OFFLINE"] != "1" {
			t.Fatalf("%s: not offline", p.Instance)
		}
		if n, ok := strings.CutPrefix(p.Instance, GroupML+"-"); ok && env["MAKTABA_STT_SLOT"] != "stt-"+n {
			t.Fatalf("%s: slot %q", p.Instance, env["MAKTABA_STT_SLOT"])
		}
	}
}
