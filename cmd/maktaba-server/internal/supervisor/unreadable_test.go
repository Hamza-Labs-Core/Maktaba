package supervisor

import (
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// A node config that cannot be read when a role's environment is made (serve checked it
// a moment before; a write that does not end, a file another program keeps locked, a
// broken one since) never starts the role as the home against this machine's own
// database: the api, streaming and the pipeline do not start, and say why; the relay agent, which only the home runs, is not wanted. A TV slot
// reads no node config, and a forced home ([server] role = "home") never does.
func TestAnUnreadableNodeConfigStartsNoRoleAsTheHome(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "")
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	unreadable := errors.New("node config C:/x/node.json: being written (a write still under way)")
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{Err: unreadable} }

	var cfg config.Config
	cfg.Server.DataDir = t.TempDir()
	cfg.Server.StreamAddr = ":8081"
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	cfg.Cloud.Enabled, cfg.Cloud.ClaimCode, cfg.Cloud.Slug = true, "code", "slug"
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if env, err := ChildEnv(role, cfg); !errors.Is(err, unreadable) || env != nil {
			t.Errorf("%s: env %v, err %v; want no environment and the node config's error", role, env, err)
		}
		if _, err := Build(role, Options{Config: cfg}); !errors.Is(err, unreadable) {
			t.Errorf("%s: Build = %v, want the node config's error", role, err)
		}
	}
	if RelayWanted(cfg) {
		t.Error("the relay agent is wanted beside a node config that cannot be read")
	}
	if _, err := ChildEnv(RoleTVSlot, cfg); err != nil {
		t.Errorf("a TV slot: %v", err)
	}
	cfg.Server.Role = "home"
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if _, err := ChildEnv(role, cfg); err != nil {
			t.Errorf("a forced home's %s: %v", role, err)
		}
	}
}
