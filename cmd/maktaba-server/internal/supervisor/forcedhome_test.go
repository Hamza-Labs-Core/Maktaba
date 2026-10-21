package supervisor

import (
	"errors"
	"os"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// TestAForcedHomeRunsNoRoleAsANode: [server] role = "home" beside a joined
// node.json (on Windows, say, another install's in the roaming profile). Not
// adding the node's variables was not enough: streaming and the pipeline read
// the node config themselves. Every role is told it is the home and pointed at
// a node config that is never written; each keeps the home's own database and
// the secrets key, and the home's TV slots and bundled Postgres are its own.
func TestAForcedHomeRunsNoRoleAsANode(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "")
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	joined := config.NodeConfig{NodeID: "6f1c9a2e-0000-4000-8000-000000000001", PrivateKey: "AQID",
		DatabaseURL: "postgres://node_6f1c9a2e@home:5432/maktaba", Home: config.HomeRef{URL: "http://home:8080"}}
	nodeConfig = func() config.NodeConfig { return joined }

	var cfg config.Config
	cfg.Server.DataDir = t.TempDir()
	cfg.Server.StreamAddr = ":8081"
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	cfg.Server.Role = "home"
	check := func(how string) {
		t.Helper()
		for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline, RoleTVSlot} {
			m := envMap(ChildEnv(role, cfg))
			if m["MAKTABA_CLUSTER_ROLE"] != "home" || m["MAKTABA_TV_HOST_ROLE"] != "home" || m["MAKTABA_NODE_CONFIG"] != NoNodeConfigPath(cfg) {
				t.Errorf("%s, %s: not told it is the home: %v", how, role, m)
			}
			for _, k := range []string{"MAKTABA_NODE_MODE", "MAKTABA_NODE", "MAKTABA_NODE_ID", "MAKTABA_FULL_NODE", "MAKTABA_HOME_API_URL"} {
				if m[k] != "" {
					t.Errorf("%s, %s: %s=%s", how, role, k, m[k])
				}
			}
			if m["DATABASE_URL"] != cfg.Database.URL || m["MAKTABA_DATABASE_URL"] != cfg.Database.URL {
				t.Errorf("%s, %s: database %q / %q, want the home's own", how, role, m["DATABASE_URL"], m["MAKTABA_DATABASE_URL"])
			}
			if os.Getenv("MAKTABA_SECRETS_KEY") == "" && m["MAKTABA_SECRETS_KEY"] != SecretsKeyPath() {
				t.Errorf("%s, %s: secrets key %q", how, role, m["MAKTABA_SECRETS_KEY"])
			}
		}
		if NodeJoined(cfg) || NodeMode(cfg, nodeConfig()) {
			t.Errorf("%s: counted as a joined node", how)
		}
	}
	check(`[server] role = "home"`)
	if _, err := os.Stat(NoNodeConfigPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the forced home's node config exists: %v", err)
	}
	// its TV slots run as a home's do (a joined node streams in one process)
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	home := SlotsEnabled(cfg)
	nodeConfig = func() config.NodeConfig { return joined }
	if SlotsEnabled(cfg) != home {
		t.Error("a forced home's TV slots follow the node config")
	}

	// MAKTABA_ROLE=home is the same, without the setting
	cfg.Server.Role = ""
	t.Setenv("MAKTABA_ROLE", "home")
	check("MAKTABA_ROLE=home")

	// not forced: the joined machine is the node, as before
	t.Setenv("MAKTABA_ROLE", "")
	m := envMap(ChildEnv(RoleStreaming, cfg))
	if m["MAKTABA_CLUSTER_ROLE"] != "" || m["MAKTABA_TV_HOST_ROLE"] != "node" || m["MAKTABA_NODE_CONFIG"] != "" {
		t.Errorf("a joined node: %v", m)
	}
	if !NodeJoined(cfg) {
		t.Error("a joined node is not counted as one")
	}
}
