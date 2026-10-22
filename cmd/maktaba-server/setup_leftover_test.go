package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// TestAFreshSetupBesideALeftoverNodeConfig: a joined node config that no
// install here runs (one removed left it). A fresh setup is
// a new home beside it: check says so (a warning: nothing is in the way),
// the welcome's find names it, and finish pins [server] role = "home", so no
// role ever runs as that node (supervisor: a forced home).
func TestAFreshSetupBesideALeftoverNodeConfig(t *testing.T) {
	spawnsPrograms(t)
	e := newSetupEnv(t)
	node := filepath.Join(e.dir, "Roaming", "Maktaba", "node.json")
	if err := os.MkdirAll(filepath.Dir(node), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte(`{"node_id":"n-1","private_key":"AQID","database_url":"postgres://node@home/maktaba","home":{"name":"Home","url":"http://home:8080"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	prevFacts, prevGPUs, prevDB := hostFacts, hostGPUs, setupDatabase
	t.Cleanup(func() { hostFacts, hostGPUs, setupDatabase = prevFacts, prevGPUs, prevDB })
	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	hostGPUs = func() []hostinfo.GPU { return nil }
	setupDatabase = func(_ *setupRun, fn func(string) error) error {
		return fn("postgres://maktaba@127.0.0.1:54329/maktaba")
	}

	_, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	warned := false
	for _, w := range d["warnings"].([]any) {
		n := w.(map[string]any)
		params, _ := n["params"].(map[string]any)
		warned = warned || (n["code"] == "node_config_unused" && params["home"] == "Home" && params["path"] == node)
	}
	if !warned || d["ok"] != setupPlatformOK() {
		t.Fatalf("check beside a leftover node config = %v", d)
	}
	_, _, ev = runStep(t, "adopt", map[string]string{"action": "find"})
	if left, _ := data(t, ev)["leftover_node"].(map[string]any); left["home"] != "Home" || left["path"] != node {
		t.Fatalf("find = %v", data(t, ev))
	}

	if code, _, ev := runStep(t, "admin", map[string]string{"username": "owner", "password": "s3cret-long"}); code != 0 {
		t.Fatalf("admin = %d %v", code, ev)
	}
	if code, _, ev := runStep(t, "finish", map[string]any{}); code != 0 {
		t.Fatalf("finish = %d %v", code, ev)
	}
	cfg, _, err := config.Load()
	if err != nil || cfg.Server.Role != "home" {
		t.Fatalf("role %q (%v): a fresh setup beside a node config nothing runs is a home", cfg.Server.Role, err)
	}
	t.Setenv("MAKTABA_ROLE", "")
	if supervisor.NodeJoined(cfg) {
		t.Error("the new home counts as that node")
	}
	if m := envOf(mustChildEnv(t, supervisor.RoleStreaming, cfg)); m["MAKTABA_CLUSTER_ROLE"] != "home" || m["MAKTABA_NODE_ID"] != "" {
		t.Errorf("its streaming role: %v", m)
	}
}

func envOf(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

// mustChildEnv is supervisor.ChildEnv for a test whose node config can be read.
func mustChildEnv(t *testing.T, role supervisor.Role, cfg config.Config) []string {
	t.Helper()
	env, err := supervisor.ChildEnv(role, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return env
}
