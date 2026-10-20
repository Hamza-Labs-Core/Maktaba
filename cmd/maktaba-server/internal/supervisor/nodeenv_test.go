package supervisor

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

// TestStreamingNodeEnvFromNodeConfig: a joined machine's streaming role runs as a
// cluster node from its node config (Epic 32.1 AC2), no flag needed.
func TestStreamingNodeEnvFromNodeConfig(t *testing.T) {
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig {
		return config.NodeConfig{NodeID: "6f1c9a2e-0000-4000-8000-000000000001", DatabaseURL: "postgresql://node_6f1c9a2e@home:5432/maktaba"}
	}
	var cfg config.Config
	cfg.Database.URL = "postgresql://local/ignored"
	cfg.Server.StreamAddr = ":8081"
	m := envMap(ChildEnv(RoleStreaming, cfg))
	if m["DATABASE_URL"] != "postgresql://node_6f1c9a2e@home:5432/maktaba" || m["MAKTABA_TV_HOST_ROLE"] != "node" ||
		m["MAKTABA_NODE_ID"] != "6f1c9a2e-0000-4000-8000-000000000001" {
		t.Fatalf("node env not applied: %v", m)
	}
	// the api keeps the home's own database and learns the streaming loopback URL
	a := envMap(ChildEnv(RoleAPI, cfg))
	if a["DATABASE_URL"] != "postgresql://local/ignored" || a["MAKTABA_STREAMING_HTTP_URL"] != "http://127.0.0.1:8081" {
		t.Fatalf("api env: %v", a)
	}
	// not joined: nothing changes
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{NodeID: "x"} }
	if m := envMap(ChildEnv(RoleStreaming, cfg)); m["MAKTABA_TV_HOST_ROLE"] != "" || m["DATABASE_URL"] != "postgresql://local/ignored" {
		t.Fatalf("unjoined machine got node env: %v", m)
	}
}

// TestStreamingCacheUnderDataDir: streaming's cache defaults to /var/maktaba (the
// containers' path), which a user cannot create on macOS or Windows, and every
// channel request failed with "mkdir /var/maktaba: permission denied". The
// supervisor puts it under the install's data directory on every OS.
func TestStreamingCacheUnderDataDir(t *testing.T) {
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	var cfg config.Config
	cfg.Server.DataDir = filepath.Join("C:", "Users", "x", "AppData", "Local", "Maktaba")
	m := envMap(ChildEnv(RoleStreaming, cfg))
	if want := filepath.Join(cfg.Server.DataDir, "cache", "streaming"); m["MAKTABA_CACHE_ROOT"] != want {
		t.Fatalf("MAKTABA_CACHE_ROOT = %q, want %q", m["MAKTABA_CACHE_ROOT"], want)
	}
	// an operator's own (inherited with the rest of the environment) is not overridden
	t.Setenv("MAKTABA_CACHE_ROOT", "/srv/own")
	if v, ok := envMap(ChildEnv(RoleStreaming, cfg))["MAKTABA_CACHE_ROOT"]; ok {
		t.Fatalf("overrode the operator's cache root with %q", v)
	}
	if _, ok := envMap(ChildEnv(RoleAPI, cfg))["MAKTABA_CACHE_ROOT"]; ok {
		t.Fatalf("only streaming gets the streaming cache root")
	}
}

// TestChildEnvStateDirs (Epic 36 story 36.2): models, the semantic-search index and
// the streaming cache sit in the install's state root; on a Mac that is the parent of
// data_dir (~/Library/Application Support/Maktaba), elsewhere data_dir itself.
func TestChildEnvStateDirs(t *testing.T) {
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	t.Setenv("MAKTABA_CHROMA_DIR", "")
	t.Setenv("HOME", t.TempDir())
	var cfg config.Config
	cfg.Server.DataDir = config.LocalDirs().Data
	root := cfg.StateDir()
	if runtime.GOOS == "darwin" && root != filepath.Dir(cfg.Server.DataDir) {
		t.Fatalf("StateDir = %s on a Mac, want the parent of data/", root)
	}
	m := envMap(ChildEnv(RolePipeline, cfg))
	if m["MAKTABA_MODELS_DIR"] != filepath.Join(root, "models") || m["MAKTABA_CHROMA_DIR"] != filepath.Join(root, "chroma") {
		t.Fatalf("models/chroma = %q / %q, want under %s", m["MAKTABA_MODELS_DIR"], m["MAKTABA_CHROMA_DIR"], root)
	}
	if got := envMap(ChildEnv(RoleStreaming, cfg))["MAKTABA_CACHE_ROOT"]; got != filepath.Join(root, "cache", "streaming") {
		t.Fatalf("MAKTABA_CACHE_ROOT = %q", got)
	}
	t.Setenv("MAKTABA_CHROMA_DIR", "/own/chroma")
	if v, ok := envMap(ChildEnv(RolePipeline, cfg))["MAKTABA_CHROMA_DIR"]; ok {
		t.Fatalf("overrode the operator's chroma dir with %q", v)
	}
}
