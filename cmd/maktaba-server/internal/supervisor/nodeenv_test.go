package supervisor

import (
	"os"
	"path/filepath"
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
	t.Setenv("MAKTABA_ROLE", "")
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

// TestPipelineFullNodeEnv: a full node's pipeline (and its extra transcription
// instances) runs as the node against the home's database; it started as a home
// worker against [database].url, a local Postgres the node never runs.
func TestPipelineFullNodeEnv(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "") // the shell's would force the home below
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig {
		return config.NodeConfig{NodeID: "6f1c9a2e-0000-4000-8000-000000000001",
			DatabaseURL: "postgres://node_6f1c9a2e@home:5432/maktaba?sslmode=prefer",
			Home:        config.HomeRef{URL: "http://home:8080"}}
	}
	var cfg config.Config
	cfg.Database.URL = "postgresql://local/ignored"
	cfg.Pipeline.Instances = 1
	m := envMap(ChildEnv(RolePipeline, cfg))
	if m["MAKTABA_DATABASE_URL"] != "postgres://node_6f1c9a2e@home:5432/maktaba?sslmode=prefer" ||
		m["DATABASE_URL"] != m["MAKTABA_DATABASE_URL"] || m["MAKTABA_NODE"] != "1" {
		t.Fatalf("pipeline node env not applied: %v", m)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("instances: %v (%d)", err, len(procs))
	}
	for _, p := range procs {
		if e := envMap(p.Cmd.Env); e["MAKTABA_NODE"] != "1" || e["MAKTABA_DATABASE_URL"] != m["MAKTABA_DATABASE_URL"] {
			t.Fatalf("transcription instance not a node: %v", e)
		}
	}
	// [server] role = "home" keeps a joined machine a home worker
	cfg.Server.Role = "home"
	if m := envMap(ChildEnv(RolePipeline, cfg)); m["MAKTABA_NODE"] != "" || m["MAKTABA_DATABASE_URL"] != "postgresql://local/ignored" {
		t.Fatalf("forced home got node env: %v", m)
	}
}

// TestCheckNodeConfigPartial: a joined node.json without node_id or private_key gave
// the pipeline MAKTABA_NODE=1 and a KeyError on every restart; serve now refuses it
// once, naming the file and what it lacks.
func TestCheckNodeConfigPartial(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "")
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(t.TempDir(), "node.json"))
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	full := config.NodeConfig{NodeID: "6f1c9a2e-0000-4000-8000-000000000001", PrivateKey: "AQID",
		DatabaseURL: "postgres://node_6f1c9a2e@home:5432/maktaba?sslmode=prefer", Home: config.HomeRef{URL: "http://home:8080"}}
	var cfg config.Config

	nodeConfig = func() config.NodeConfig { return full }
	if err := CheckNodeConfig(cfg); err != nil {
		t.Fatalf("a complete node config: %v", err)
	}
	partial := full
	partial.NodeID, partial.PrivateKey = "", " "
	nodeConfig = func() config.NodeConfig { return partial }
	err := CheckNodeConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "node_id or private_key") ||
		!strings.Contains(err.Error(), config.LocalNodeConfigPath()) || strings.Contains(err.Error(), "sslmode") {
		t.Fatalf("partial node config: %v", err)
	}
	// [server] role = "home" ignores node.json; an unjoined one is no node
	cfg.Server.Role = "home"
	if err := CheckNodeConfig(cfg); err != nil {
		t.Fatalf("forced home: %v", err)
	}
	cfg.Server.Role = ""
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{NodeID: "x"} }
	if err := CheckNodeConfig(cfg); err != nil {
		t.Fatalf("not joined: %v", err)
	}

	// a node.json that does not parse is not "not joined": that started the node as a
	// home, migrating its own database
	path := config.LocalNodeConfigPath()
	if err := os.WriteFile(path, []byte(`{"node_id": "6f1c9a2e", "database_url": "postgres://n`), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeConfig = config.LocalNodeConfig
	if err := CheckNodeConfig(cfg); err == nil || !strings.Contains(err.Error(), path) ||
		!strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("broken node.json: %v", err)
	}
	cfg.Server.Role = "home"
	if err := CheckNodeConfig(cfg); err != nil {
		t.Fatalf("forced home with a broken node.json: %v", err)
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
