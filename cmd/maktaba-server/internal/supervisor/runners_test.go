package supervisor

import (
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
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
